package scoring

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/features"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

var cols = []string{"Column_0", "Column_1", "Column_2", "Column_3", "Column_4", "Column_5"}

// testProfile scores with the LightGBM fixture model (six numeric features)
// plus one real-time feature the model does not use.
func testProfile(t *testing.T) (profile.Profile, string) {
	t.Helper()
	schema := profile.NewSchema(cols, nil)
	p := profile.Profile{Name: "test", Schema: schema, Features: []profile.Feature{
		{Name: "n", Kind: profile.Count, Entity: "card", Windows: []time.Duration{time.Hour}},
	}}
	dir := t.TempDir()
	model, err := os.ReadFile("../model/lgbm/testdata/default.txt")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "model.txt"), model, 0o644)
	manifest, _ := json.Marshal(map[string]any{
		"profile": "test", "schema_id": schema.ID, "features": cols, "categorical": []string{},
		"test_budgets": map[string]any{"0.005": map[string]float64{"threshold": 0.9}, "0.02": map[string]float64{"threshold": 0.5}},
	})
	os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644)
	return p, dir
}

func event(p profile.Profile, id string, v float64) *unfraudv1.Envelope {
	num := make([]float64, len(cols))
	for i := range num {
		num[i] = v
	}
	return profile.NewEvent("test", 1000, &unfraudv1.Event{EventId: id, Entities: map[string]string{"card": "c"},
		SchemaId: p.Schema.ID, Num: num})
}

type downState struct{}

func (downState) Exchange(context.Context, features.Guard, []features.Query, []features.Result, []features.Update) error {
	return fmt.Errorf("%w: connection refused", features.ErrUnavailable)
}
func (downState) Apply(context.Context, features.Guard, []features.Update) error {
	return fmt.Errorf("%w: connection refused", features.ErrUnavailable)
}
func (downState) Expire(context.Context, int64) error { return nil }

func TestScoringDegradesWhenStateIsDown(t *testing.T) {
	p, dir := testProfile(t)
	healthy, err := New(p, features.NewMemory(), dir, DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	down, err := New(p, downState{}, dir, DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := healthy.Process(context.Background(), event(p, "e", 1.5))
	got, err := down.Process(context.Background(), event(p, "e", 1.5))
	if err != nil {
		t.Fatalf("an unreachable state must not stop scoring: %v", err)
	}
	if !got.Degraded || want.Degraded {
		t.Fatalf("degraded flags: down %v, healthy %v", got.Degraded, want.Degraded)
	}
	// The model ignores the real-time feature, so the score is unaffected.
	if got.Probability != want.Probability {
		t.Fatalf("degraded probability %v, healthy %v", got.Probability, want.Probability)
	}
	// Non-Event records are skipped, not fatal.
	release := &unfraudv1.Envelope{Payload: &unfraudv1.Envelope_LabelRelease{LabelRelease: &unfraudv1.LabelRelease{
		EventId: "e", Label: unfraudv1.Label_LABEL_FRAUD}}}
	if d, err := down.Process(context.Background(), release); d != nil || err != nil {
		t.Fatalf("label release while down: %v, %v", d, err)
	}
}

func TestShadowModelIsRecordedNotActedOn(t *testing.T) {
	p, dir := testProfile(t)
	cfg := DefaultConfig
	cfg.ShadowDir = dir
	sc, err := New(p, features.NewMemory(), dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	d, err := sc.Process(context.Background(), event(p, "e", 0.3))
	if err != nil {
		t.Fatal(err)
	}
	if d.Shadow == nil || math.Abs(d.Shadow.Probability-d.Probability) > 1e-12 || d.Shadow.Action != d.ModelAction {
		t.Fatalf("shadow %+v vs active %v/%v", d.Shadow, d.Probability, d.ModelAction)
	}
}
