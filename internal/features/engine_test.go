package features

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

var testSchema = profile.NewSchema([]string{"amt"}, []string{"ip"})

func testEngine(t *testing.T, feats ...profile.Feature) *Engine {
	t.Helper()
	e, err := New(profile.Profile{Name: "test", Schema: testSchema, Features: feats}, NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	return e
}

const minute = int64(time.Minute / time.Millisecond)

type ev struct {
	at     int64 // minutes
	card   string
	device string
	amt    float64
	ip     string
}

func (x ev) env() *unfraudv1.Envelope {
	ents := map[string]string{"card": x.card}
	if x.device != "" {
		ents["device"] = x.device
	}
	num := testSchema.NewNum()
	if x.amt != 0 {
		num[0] = x.amt
	}
	cat := testSchema.NewCat()
	cat[0] = x.ip
	id := fmt.Sprintf("%s@%d", x.card, x.at)
	return profile.NewEvent("test", x.at*minute, &unfraudv1.Event{EventId: id, Entities: ents, SchemaId: testSchema.ID, Num: num, Cat: cat})
}

// run feeds events and returns each Event's features.
func run(t *testing.T, e *Engine, envs ...*unfraudv1.Envelope) [][]float64 {
	t.Helper()
	var out [][]float64
	for _, env := range envs {
		v, err := e.Process(context.Background(), env)
		if err != nil {
			t.Fatal(err)
		}
		if v != nil {
			out = append(out, v)
		}
	}
	return out
}

func check(t *testing.T, cols []string, got [][]float64, want [][]float64) {
	t.Helper()
	for i := range want {
		for j := range want[i] {
			g, w := got[i][j], want[i][j]
			if !(g == w || math.IsNaN(g) && math.IsNaN(w)) {
				t.Errorf("event %d %s = %v, want %v", i, cols[j], g, w)
			}
		}
	}
}

var nan = math.NaN()

func TestHistoryExcludesTheCurrentEvent(t *testing.T) {
	e := testEngine(t,
		profile.Feature{Name: "n", Kind: profile.Count, Entity: "card", Windows: []time.Duration{time.Hour}},
		profile.Feature{Name: "amt", Kind: profile.Sum, Entity: "card", Field: "amt", Windows: []time.Duration{time.Hour}},
		profile.Feature{Name: "ratio", Kind: profile.RatioToMean, Entity: "card", Field: "amt", Windows: []time.Duration{time.Hour}},
	)
	got := run(t, e,
		ev{at: 0, card: "a", amt: 10}.env(),
		ev{at: 5, card: "a", amt: 30}.env(),
		ev{at: 6, card: "b", amt: 7}.env(),
		ev{at: 10, card: "a", amt: 40}.env(),
		ev{at: 200, card: "a", amt: 5}.env(), // the hour window has emptied
	)
	check(t, e.Columns(), got, [][]float64{
		{0, 0, nan},
		{1, 10, 3},
		{0, 0, nan},
		{2, 40, 2},
		{0, 0, nan},
	})
}

func TestWindowsAreBucketed(t *testing.T) {
	e := testEngine(t, profile.Feature{Name: "n", Kind: profile.Count, Entity: "card", Windows: []time.Duration{24 * time.Hour}})
	// Buckets are an hour long. An Event 23h05m later still sees the first
	// one, because its bucket is the oldest of the 24 the window covers.
	got := run(t, e,
		ev{at: 0, card: "a"}.env(),
		ev{at: 23*60 + 5, card: "a"}.env(),
		ev{at: 24*60 + 5, card: "a"}.env(),
	)
	check(t, e.Columns(), got, [][]float64{{0}, {1}, {1}})
}

func TestDistinctAndNewPair(t *testing.T) {
	e := testEngine(t,
		profile.Feature{Name: "devices", Kind: profile.Distinct, Entity: "card", Of: "device", Windows: []time.Duration{time.Hour}},
		profile.Feature{Name: "new_device", Kind: profile.NewPair, Entity: "card", Of: "device"},
	)
	got := run(t, e,
		ev{at: 0, card: "a", device: "d1"}.env(),
		ev{at: 1, card: "a", device: "d2"}.env(),
		ev{at: 2, card: "a", device: "d1"}.env(),
		ev{at: 3, card: "a"}.env(),                // no device: NewPair is unknown
		ev{at: 90, card: "a", device: "d1"}.env(), // window emptied, pair still known
	)
	check(t, e.Columns(), got, [][]float64{
		{0, 1},
		{1, 1},
		{2, 0},
		{2, nan},
		{0, 0},
	})
}

func TestPreviousValueFeatures(t *testing.T) {
	e := testEngine(t,
		profile.Feature{Name: "since", Kind: profile.SinceLast, Entity: "card"},
		profile.Feature{Name: "ip_changed", Kind: profile.Changed, Entity: "card", Field: "ip"},
		profile.Feature{Name: "amt_delta", Kind: profile.Delta, Entity: "card", Field: "amt"},
	)
	got := run(t, e,
		ev{at: 0, card: "a", amt: 10, ip: "x"}.env(),
		ev{at: 2, card: "a", ip: "x"}.env(), // amount missing: no delta, kept 10
		ev{at: 5, card: "a", amt: 4, ip: "y"}.env(),
	)
	check(t, e.Columns(), got, [][]float64{
		{nan, nan, nan},
		{120, 0, nan},
		{180, 1, -6},
	})
}

func TestFraudLabelsCountOnlyReleasedFraud(t *testing.T) {
	e := testEngine(t, profile.Feature{Name: "fraud", Kind: profile.FraudLabels, Entity: "card", Windows: []time.Duration{24 * time.Hour}})
	release := func(at int64, l unfraudv1.Label, card string) *unfraudv1.Envelope {
		return &unfraudv1.Envelope{EventTimeMs: at * minute, Payload: &unfraudv1.Envelope_LabelRelease{
			LabelRelease: &unfraudv1.LabelRelease{EventId: fmt.Sprint(at), Label: l, Entities: map[string]string{"card": card}}}}
	}
	got := run(t, e,
		ev{at: 0, card: "a"}.env(),
		release(10, unfraudv1.Label_LABEL_FRAUD, "a"),
		release(11, unfraudv1.Label_LABEL_LEGIT, "a"),
		release(12, unfraudv1.Label_LABEL_FRAUD, "b"),
		ev{at: 20, card: "a"}.env(),
	)
	check(t, e.Columns(), got, [][]float64{{0}, {1}})
}

func TestMissingEntityGivesNaNAndNoState(t *testing.T) {
	e := testEngine(t, profile.Feature{Name: "n", Kind: profile.Count, Entity: "device", Windows: []time.Duration{time.Hour}})
	got := run(t, e,
		ev{at: 0, card: "a"}.env(),
		ev{at: 1, card: "a", device: "d"}.env(),
	)
	check(t, e.Columns(), got, [][]float64{{nan}, {0}})
}

func TestExpireForgetsOnlyInvisibleState(t *testing.T) {
	st := NewMemory()
	e, err := New(profile.Profile{Name: "test", Schema: testSchema, Features: []profile.Feature{
		{Name: "n", Kind: profile.Count, Entity: "card", Windows: []time.Duration{time.Hour}},
		{Name: "devices", Kind: profile.Distinct, Entity: "card", Of: "device", Windows: []time.Duration{time.Hour}},
	}}, st)
	if err != nil {
		t.Fatal(err)
	}
	run(t, e, ev{at: 0, card: "a", device: "d"}.env())
	ctx := context.Background()
	if err := st.Expire(ctx, 30*minute); err != nil {
		t.Fatal(err)
	}
	if s, z, _ := st.Size(); s != 1 || z != 1 {
		t.Fatalf("state expired while still visible: %d series, %d sets", s, z)
	}
	if err := st.Expire(ctx, 3*60*minute); err != nil {
		t.Fatal(err)
	}
	if s, z, _ := st.Size(); s != 0 || z != 0 {
		t.Fatalf("state kept after it became invisible: %d series, %d sets", s, z)
	}
}

func TestInvalidFeatureRejected(t *testing.T) {
	_, err := New(profile.Profile{Name: "test", Schema: testSchema, Features: []profile.Feature{
		{Name: "bad", Kind: profile.Sum, Entity: "card", Field: "nope", Windows: []time.Duration{time.Hour}},
	}}, NewMemory())
	if err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestLabelReleaseWithoutEntitiesFails(t *testing.T) {
	e := testEngine(t, profile.Feature{Name: "fraud", Kind: profile.FraudLabels, Entity: "card", Windows: []time.Duration{24 * time.Hour}})
	env := &unfraudv1.Envelope{EventTimeMs: 0, Payload: &unfraudv1.Envelope_LabelRelease{
		LabelRelease: &unfraudv1.LabelRelease{EventId: "x", Label: unfraudv1.Label_LABEL_FRAUD}}}
	if _, err := e.Process(context.Background(), env); err == nil {
		t.Fatal("a fraud label release without entities must be rejected")
	}
}

func TestRedeliveredRecordsUpdateStateOnce(t *testing.T) {
	e := testEngine(t,
		profile.Feature{Name: "n", Kind: profile.Count, Entity: "card", Windows: []time.Duration{time.Hour}},
		profile.Feature{Name: "fraud", Kind: profile.FraudLabels, Entity: "card", Windows: []time.Duration{time.Hour}},
	)
	first := ev{at: 0, card: "a"}.env()
	release := &unfraudv1.Envelope{EventTimeMs: 1 * minute, Payload: &unfraudv1.Envelope_LabelRelease{
		LabelRelease: &unfraudv1.LabelRelease{EventId: "a@0", Label: unfraudv1.Label_LABEL_FRAUD, Entities: map[string]string{"card": "a"}}}}
	second := ev{at: 2, card: "a"}.env()
	got := run(t, e, first, release, first, release, second)
	// The redelivered first Event sees its own earlier update (n=1), but
	// neither it nor the redelivered release is counted again.
	check(t, e.Columns(), got, [][]float64{{0, 0}, {1, 1}, {1, 1}})
}
