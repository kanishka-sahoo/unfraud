package graph

import (
	"math"
	"testing"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

func TestFlowFeaturesAtPeriodClose(t *testing.T) {
	schema := profile.NewSchema([]string{"v"}, nil)
	f, err := NewFlow(profile.FlowConfig{Node: "tx", Aggregate: []string{"v"}}, schema)
	if err != nil {
		t.Fatal(err)
	}
	node := func(id string, v float64) *unfraudv1.Envelope {
		return &unfraudv1.Envelope{EventTimeMs: 100, Payload: &unfraudv1.Envelope_Event{Event: &unfraudv1.Event{
			EventId: id, Entities: map[string]string{"tx": id}, Num: []float64{v}}}}
	}
	link := func(a, b string) *unfraudv1.Envelope {
		return &unfraudv1.Envelope{EventTimeMs: 100, Payload: &unfraudv1.Envelope_Link{Link: &unfraudv1.Link{
			Src: &unfraudv1.EntityRef{Type: "tx", Id: a}, Dst: &unfraudv1.EntityRef{Type: "tx", Id: b}}}}
	}
	// a -> b -> c, a -> d; e is isolated. Links arrive interleaved with,
	// and partly before, their Events, as when partitions are merged.
	for _, env := range []*unfraudv1.Envelope{
		link("a", "b"), node("a", 1), link("b", "c"), node("b", 2), node("c", 3),
		link("a", "d"), node("d", 4), node("e", 5),
	} {
		f.Process(env)
	}
	if r := f.Revised(); len(r) != 0 {
		t.Fatalf("revised before the period closed: %v", r)
	}
	f.Process(&unfraudv1.Envelope{EventTimeMs: 150, Payload: &unfraudv1.Envelope_Watermark{Watermark: &unfraudv1.Watermark{}}})
	got := map[string][]float64{}
	for _, r := range f.Revised() {
		got[r.EventID] = r.Values
		if r.ClosedMs != 150 || r.PeriodMs != 100 {
			t.Fatalf("times %+v", r)
		}
	}
	// columns: in, out, in2, out2, mean neighbour v
	want := map[string][]float64{
		"a": {0, 2, 0, 1, 3}, // pays b and d; reaches c in two hops
		"b": {1, 1, 0, 0, 2}, // neighbours a and c
		"c": {1, 0, 1, 0, 2}, // paid by b, which a paid
		"d": {1, 0, 0, 0, 1},
		"e": {0, 0, 0, 0, math.NaN()},
	}
	for id, w := range want {
		for i := range w {
			if g := got[id][i]; !(g == w[i] || math.IsNaN(g) && math.IsNaN(w[i])) {
				t.Errorf("%s column %s = %v, want %v", id, f.Columns()[i], g, w[i])
			}
		}
	}
}
