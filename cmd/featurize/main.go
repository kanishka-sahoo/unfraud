// Command featurize runs an Accuracy Run: it replays a Profile's dataset
// through the feature engine in-process, so every feature is exact as of
// Event Time, and writes training tables (ADR-0005, ADR-0006):
//
//	<out>/features.parquet  one row per Event: ids, raw schema features, real-time features
//	<out>/labels.parquet    one row per Label Release
//	<out>/manifest.json     column roles and run parameters
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/features"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
	"github.com/kanishka-sahoo/unfraud/internal/replay"
	"github.com/kanishka-sahoo/unfraud/internal/table"
)

// Manifest describes a features table. The training CLI reads it to tell raw
// columns from real-time ones.
type Manifest struct {
	Profile      string    `json:"profile"`
	SchemaID     uint32    `json:"schema_id"`
	RawNum       []string  `json:"raw_num"`
	RawCat       []string  `json:"raw_cat"`
	Realtime     []string  `json:"realtime"`
	LabelDelay   string    `json:"label_delay"`
	Events       int       `json:"events"`
	Labels       int       `json:"labels"`
	FirstEventMs int64     `json:"first_event_ms"`
	LastEventMs  int64     `json:"last_event_ms"`
	GitRevision  string    `json:"git_revision"`
	CreatedAt    time.Time `json:"created_at"`
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("featurize failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	var (
		profileName = flag.String("profile", "", "profile: "+strings.Join(registry.Names(), ", "))
		dataDir     = flag.String("data", "data/raw", "root folder with one subfolder per profile")
		outDir      = flag.String("out", "", "output folder (default data/features/<profile>)")
		limit       = flag.Int("limit", 0, "stop after this many dataset records; 0 = all")
		labelDelay  = flag.Duration("label-delay", 0, "override the profile's label delay")
	)
	flag.Parse()

	p, err := registry.Get(*profileName)
	if err != nil {
		return err
	}
	if *labelDelay > 0 {
		p.LabelDelay = *labelDelay
	}
	if *outDir == "" {
		*outDir = filepath.Join("data", "features", p.Name)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	state := features.NewMemory()
	eng, err := features.New(p, state)
	if err != nil {
		return err
	}
	sink, err := newSink(*outDir, p, eng)
	if err != nil {
		return err
	}

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
		ProgressEvery: 10 * time.Second,
		Progress: func(s replay.Stats) {
			series, sets, lasts := state.Size()
			log.Info("progress", "events", s.Events, "event_time", time.UnixMilli(s.LastMs).UTC().Format(time.RFC3339),
				"state_series", series, "state_sets", sets, "state_lasts", lasts)
		},
	})
	if err != nil {
		sink.close()
		return err
	}

	m := Manifest{
		Profile: p.Name, SchemaID: p.Schema.ID, RawNum: p.Schema.Num, RawCat: p.Schema.Cat,
		Realtime: eng.Columns(), LabelDelay: p.LabelDelay.String(),
		Events: stats.Events, Labels: sink.labels, FirstEventMs: sink.firstMs, LastEventMs: sink.lastMs,
		GitRevision: revision(), CreatedAt: time.Now().UTC(),
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*outDir, "manifest.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	log.Info("featurize finished", "out", *outDir, "events", stats.Events, "labels", sink.labels,
		"realtime_features", len(eng.Columns()), "elapsed", time.Since(start).Round(time.Millisecond).String())
	return nil
}

// sink feeds the replay stream through the Engine into the two tables.
type sink struct {
	eng             *features.Engine
	schema          *profile.Schema
	feats, labelsW  *table.Writer
	nNum, nCat      int
	labels          int
	firstMs, lastMs int64
	started         bool
}

func newSink(dir string, p profile.Profile, eng *features.Engine) (*sink, error) {
	cols := []table.Column{{Name: "event_id", Type: table.String}, {Name: "event_time_ms", Type: table.Int64}}
	for _, n := range p.Schema.Num {
		cols = append(cols, table.Column{Name: n, Type: table.Float32})
	}
	for _, n := range p.Schema.Cat {
		cols = append(cols, table.Column{Name: n, Type: table.String})
	}
	for _, n := range eng.Columns() {
		cols = append(cols, table.Column{Name: n, Type: table.Float32})
	}
	feats, err := table.Create(filepath.Join(dir, "features.parquet"), cols)
	if err != nil {
		return nil, err
	}
	labels, err := table.Create(filepath.Join(dir, "labels.parquet"), []table.Column{
		{Name: "event_id", Type: table.String}, {Name: "label", Type: table.Int64},
		{Name: "event_time_ms", Type: table.Int64}, {Name: "release_time_ms", Type: table.Int64},
	})
	if err != nil {
		feats.Close()
		return nil, err
	}
	return &sink{eng: eng, schema: p.Schema, feats: feats, labelsW: labels, nNum: len(p.Schema.Num), nCat: len(p.Schema.Cat)}, nil
}

func (s *sink) Send(ctx context.Context, o replay.Out) error {
	rt, err := s.eng.Process(ctx, o.Env)
	if err != nil {
		return err
	}
	switch p := o.Env.Payload.(type) {
	case *unfraudv1.Envelope_Event:
		return s.writeEvent(o.Env.EventTimeMs, p.Event, rt)
	case *unfraudv1.Envelope_LabelRelease:
		lr := p.LabelRelease
		s.labelsW.SetString(0, lr.EventId)
		s.labelsW.SetInt64(1, map[unfraudv1.Label]int64{unfraudv1.Label_LABEL_FRAUD: 1}[lr.Label])
		s.labelsW.SetInt64(2, lr.LabelledEventTimeMs)
		s.labelsW.SetInt64(3, o.Env.EventTimeMs)
		s.labels++
		return s.labelsW.WriteRow()
	}
	return nil
}

func (s *sink) writeEvent(t int64, ev *unfraudv1.Event, rt []float64) error {
	if ev.SchemaId != s.schema.ID {
		return fmt.Errorf("event %s has schema %d, want %d", ev.EventId, ev.SchemaId, s.schema.ID)
	}
	if !s.started {
		s.started, s.firstMs = true, t
	}
	s.lastMs = t
	w := s.feats
	w.SetString(0, ev.EventId)
	w.SetInt64(1, t)
	col := 2
	for _, v := range ev.Num {
		if !math.IsNaN(v) {
			w.SetFloat32(col, float32(v))
		}
		col++
	}
	for _, v := range ev.Cat {
		if v != "" {
			w.SetString(col, v)
		}
		col++
	}
	for _, v := range rt {
		if !math.IsNaN(v) {
			w.SetFloat32(col, float32(v))
		}
		col++
	}
	return w.WriteRow()
}

func (s *sink) Flush(context.Context) error { return s.close() }

func (s *sink) close() error {
	err1 := s.feats.Close()
	err2 := s.labelsW.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

func revision() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, kv := range bi.Settings {
			if kv.Key == "vcs.revision" {
				return kv.Value
			}
		}
	}
	return "unknown"
}
