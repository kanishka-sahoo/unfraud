// Command accuracy runs an Accuracy Run through the full scorer (real-time
// features, model, Rules) with exact in-process state, and writes every
// Decision to <out>/decisions.parquet for evaluation (ADR-0006).
package main

import (
	"context"
	"flag"
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
	)
	flag.Parse()
	p, err := registry.Get(*profileName)
	if err != nil {
		return err
	}
	if *out == "" {
		*out = *modelDir
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sc, err := scoring.New(p, features.NewMemory(), *modelDir, scoring.DefaultConfig)
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
	sink := &decisionSink{sc: sc, w: w}

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
	log.Info("accuracy run finished", "events", stats.Events, "flagged", sink.flagged,
		"elapsed", time.Since(start).Round(time.Millisecond).String(), "out", *out)
	return nil
}

type decisionSink struct {
	sc      *scoring.Scorer
	w       *table.Writer
	flagged int
}

func (s *decisionSink) Send(ctx context.Context, o replay.Out) error {
	d, err := s.sc.Process(ctx, o.Env)
	if err != nil || d == nil {
		return err
	}
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

func (s *decisionSink) Flush(context.Context) error { return s.w.Close() }
