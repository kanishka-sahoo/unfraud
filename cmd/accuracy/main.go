// Command accuracy runs an Accuracy Run through the full scorer (real-time
// features, model, Rules) with exact in-process state, and writes every
// Decision to <out>/decisions.parquet for evaluation (ADR-0006).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/features"
	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
	"github.com/kanishka-sahoo/unfraud/internal/replay"
	"github.com/kanishka-sahoo/unfraud/internal/scoring"
	"github.com/kanishka-sahoo/unfraud/internal/table"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("accuracy run failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	var (
		profileName = flag.String("profile", "", "profile: "+strings.Join(registry.Names(), ", "))
		dataDir     = flag.String("data", "data/raw", "root folder with one subfolder per profile")
		modelDir    = flag.String("model", "", "trained model folder")
		out         = flag.String("out", "", "output folder (default: the model folder)")
		limit       = flag.Int("limit", 0, "stop after this many dataset records; 0 = all")
		ringPolicy  = flag.String("ring-policy", "", "override the Ring policy: minNodes,minFraud,minShare")
		evaluated   = flag.Bool("evaluated-only", false, "decide only from the validation period on; earlier Events still build state")
	)
	flag.Parse()
	p, err := registry.Get(*profileName)
	if err != nil {
		return err
	}
	if *ringPolicy != "" && p.Graph != nil {
		g := *p.Graph
		if _, err := fmt.Sscanf(*ringPolicy, "%d,%d,%g", &g.RingMinNodes, &g.RingMinFraud, &g.RingMinShare); err != nil {
			return fmt.Errorf("-ring-policy: %w", err)
		}
		p.Graph = &g
	}
	if *out == "" {
		*out = *modelDir
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := scoring.DefaultConfig
	if *evaluated {
		cfg.ScoreFromMs = p.Split.Validation.UnixMilli()
	}
	sc, err := scoring.New(p, features.NewMemory(), *modelDir, cfg, features.WithGraph(p))
	if err != nil {
		return err
	}
	w, err := table.Create(filepath.Join(*out, "decisions.parquet"), []table.Column{
		{Name: "event_id", Type: table.String}, {Name: "event_time_ms", Type: table.Int64},
		{Name: "probability", Type: table.Float32}, {Name: "model_action", Type: table.Int64},
		{Name: "rule_action", Type: table.Int64}, {Name: "action", Type: table.Int64},
		{Name: "reasons", Type: table.String},
	})
	if err != nil {
		return err
	}
	rw, err := table.Create(filepath.Join(*out, "revisions.parquet"), []table.Column{
		{Name: "event_id", Type: table.String}, {Name: "event_time_ms", Type: table.Int64},
		{Name: "revised_at_ms", Type: table.Int64}, {Name: "ring_id", Type: table.String},
	})
	if err != nil {
		return err
	}
	sink := &decisionSink{sc: sc, w: w, rw: rw}

	dir := filepath.Join(*dataDir, p.Name)
	events, err := p.Open(dir)
	if err != nil {
		return err
	}
	defer events.Close()
	labels, err := p.Open(dir)
	if err != nil {
		return err
	}
	defer labels.Close()

	start := time.Now()
	stats, err := replay.Run(ctx, p.Name, events, labels, sink, replay.Options{
		LabelDelay: p.LabelDelay, WatermarkInterval: p.WatermarkInterval, Limit: *limit,
	})
	if err != nil {
		w.Close()
		return err
	}
	log.Info("accuracy run finished", "events", stats.Events, "flagged", sink.flagged, "revisions", sink.revisions,
		"elapsed", time.Since(start).Round(time.Millisecond).String(), "out", *out)
	return nil
}

type decisionSink struct {
	sc        *scoring.Scorer
	w, rw     *table.Writer
	flagged   int
	revisions int
}

func (s *decisionSink) Send(ctx context.Context, o replay.Out) error {
	d, err := s.sc.Process(ctx, o.Env)
	if err != nil {
		return err
	}
	for _, r := range s.sc.Revisions() {
		s.rw.SetString(0, r.EventID)
		s.rw.SetInt64(1, r.EventTimeMs)
		s.rw.SetInt64(2, r.RevisedAtMs)
		s.rw.SetString(3, r.RingID)
		if err := s.rw.WriteRow(); err != nil {
			return err
		}
		s.revisions++
	}
	if d == nil {
		return nil
	}
	return s.write(d)
}

func (s *decisionSink) write(d *scoring.Decision) error {
	if d.Action > 0 {
		s.flagged++
	}
	codes := make([]string, len(d.Hits))
	for i, h := range d.Hits {
		codes[i] = h.Code
	}
	s.w.SetString(0, d.EventID)
	s.w.SetInt64(1, d.EventTimeMs)
	s.w.SetFloat32(2, float32(d.Probability))
	s.w.SetInt64(3, int64(d.ModelAction))
	s.w.SetInt64(4, int64(d.RuleAction))
	s.w.SetInt64(5, int64(d.Action))
	s.w.SetString(6, strings.Join(codes, ","))
	return s.w.WriteRow()
}

func (s *decisionSink) Flush(context.Context) error {
	if err := s.w.Close(); err != nil {
		return err
	}
	return s.rw.Close()
}
