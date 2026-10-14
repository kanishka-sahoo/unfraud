package graph

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

const hour = int64(time.Hour / time.Millisecond)

func cfg() profile.GraphConfig {
	return profile.GraphConfig{Node: "card", Links: []string{"device"}, MaxDegree: 3,
		Window: 48 * time.Hour, Rebuild: 24 * time.Hour, FraudRetention: 1000 * time.Hour}
}

func event(at int64, card, device string) *unfraudv1.Envelope {
	ents := map[string]string{"card": card}
	if device != "" {
		ents["device"] = device
	}
	return &unfraudv1.Envelope{EventTimeMs: at * hour, Payload: &unfraudv1.Envelope_Event{
		Event: &unfraudv1.Event{EventId: card, Entities: ents}}}
}

func fraud(at int64, card string) *unfraudv1.Envelope {
	return &unfraudv1.Envelope{EventTimeMs: at * hour, Payload: &unfraudv1.Envelope_LabelRelease{
		LabelRelease: &unfraudv1.LabelRelease{Label: unfraudv1.Label_LABEL_FRAUD, Entities: map[string]string{"card": card}}}}
}

// feed returns the features of each Event.
func feed(t *testing.T, g *Graph, envs ...*unfraudv1.Envelope) [][]float64 {
	t.Helper()
	var out [][]float64
	for _, env := range envs {
		if v := g.Process(env); v != nil {
			out = append(out, v)
		}
	}
	return out
}

func newGraph(t *testing.T) *Graph {
	g, err := New(cfg())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// same compares feature vectors, treating NaN as equal to NaN.
func same(a, b []float64) bool {
	return slices.EqualFunc(a, b, func(x, y float64) bool { return x == y || math.IsNaN(x) && math.IsNaN(y) })
}

var nan = math.NaN()

func TestPayeeCountsOtherCardsReleasedFraud(t *testing.T) {
	g := newGraph(t)
	got := feed(t, g,
		event(0, "a", "d1"), // nobody else used d1
		event(1, "b", "d1"),
		fraud(2, "a"),
		event(3, "b", "d1"), // a's fraud is visible to b
		fraud(4, "b"),
		event(5, "b", "d1"), // b's own fraud is not counted for b
		event(6, "c", "d1"), // c sees both
	)
	want := [][2]float64{{0, nan}, {0, 0}, {1, 1}, {1, 1}, {2, 1}}
	for i, w := range want {
		if !same(got[i][:2], w[:]) {
			t.Errorf("event %d: payee features %v, want %v", i, got[i][:2], w)
		}
	}
}

func TestNeighborsAreCardsSharingAnyOfTheCardsAttributes(t *testing.T) {
	g := newGraph(t)
	got := feed(t, g,
		event(0, "a", "d1"), event(0, "x", "d1"),
		event(1, "b", "d1"), // b shares d1 with a and x
		fraud(2, "a"),
		event(3, "b", "d2"), // a new device: nobody else used d2, but a still shares d1
	)
	if w := []float64{0, 0}; !same(got[2][2:4], w) {
		t.Errorf("b's neighbours on d1 before the release: %v, want %v", got[2][2:4], w)
	}
	if w := []float64{0, nan, 1, 0.5}; !same(got[3][:4], w) {
		t.Errorf("b on d2: %v, want %v", got[3][:4], w)
	}
}

func TestSuperNodesAreNotNeighborhoods(t *testing.T) {
	g := newGraph(t)
	got := feed(t, g,
		event(0, "a", "shared"), event(0, "b", "shared"), event(0, "c", "shared"),
		fraud(1, "a"),
		event(2, "d", "shared"), // a fourth card: "shared" exceeds MaxDegree 3
	)
	if w := []float64{1, 1.0 / 3, 0, nan}; !same(got[3][:4], w) {
		t.Errorf("fourth card: %v, want %v: the payee still counts, the neighbourhood does not", got[3][:4], w)
	}
}

func TestRebuildDropsLinksOutsideTheWindow(t *testing.T) {
	g := newGraph(t)
	got := feed(t, g,
		event(0, "a", "d1"),
		fraud(1, "a"),
		event(40, "b", "d1"),  // within 48h of a
		event(80, "c", "d1"),  // rebuilt at 80h: a's link (0h) left the window, b's (40h) is kept
		event(200, "e", "d1"), // every earlier link has expired
	)
	want := [][2]float64{{0, nan}, {1, 1}, {0, 0}, {0, nan}}
	for i, w := range want {
		if !same(got[i][:2], w[:]) {
			t.Errorf("event %d: %v, want %v", i, got[i][:2], w)
		}
	}
}

func TestCommunitiesSplitABridgedComponent(t *testing.T) {
	g, err := New(profile.GraphConfig{Node: "card", Links: []string{"device"}, MaxDegree: 10,
		Window: 240 * time.Hour, Rebuild: 24 * time.Hour, FraudRetention: 1000 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	var envs []*unfraudv1.Envelope
	// Two dense groups: cards a1-a4 share devices da1, da2; b1-b4 share db1, db2.
	for _, grp := range []string{"a", "b"} {
		for i := 1; i <= 4; i++ {
			for _, d := range []string{"1", "2"} {
				envs = append(envs, event(0, fmt.Sprintf("%s%d", grp, i), "d"+grp+d))
			}
		}
	}
	// One bridge card uses a device from each group.
	envs = append(envs, event(1, "x", "da1"), event(1, "x", "db1"), fraud(2, "b1"), fraud(2, "b2"))
	feed(t, g, envs...)
	// After the next daily rebuild a new card on db2 joins the b community:
	// 4 cards (+ itself), 2 of them fraud.
	got := g.Process(event(30, "new", "db2"))
	if got[4] != 5 || got[5] != 0.5 {
		t.Fatalf("community cards %v, fraud share %v; want 5, 0.5", got[4], got[5])
	}
	if r := g.Rings(); r != nil {
		t.Fatalf("rings reported without a ring policy: %v", r)
	}
}

func TestRingsAreCommunitiesWithEnoughFraud(t *testing.T) {
	c := cfg()
	c.MaxDegree, c.RingMinNodes, c.RingMinFraud = 10, 3, 2
	g, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	feed(t, g,
		event(0, "a", "d1"), event(0, "b", "d1"), event(0, "c", "d1"), // a 3-card community
		event(0, "x", "d2"), event(0, "y", "d2"), event(0, "z", "d2"), // another
		fraud(1, "a"), fraud(1, "b"), fraud(1, "x"), // two fraud cards in the first only
	)
	g.Process(event(25, "later", "")) // triggers the daily rebuild
	rings := g.Rings()
	if len(rings) != 1 || rings[0].Fraud != 2 || !slices.Equal(rings[0].Nodes, []string{"a", "b", "c"}) {
		t.Fatalf("rings %+v, want one ring of a, b, c with 2 fraud", rings)
	}
	if again := g.Rings(); again != nil {
		t.Fatalf("rings returned twice: %+v", again)
	}
}

func TestNeighborhoodWalksCardsThroughDevices(t *testing.T) {
	g := newGraph(t)
	feed(t, g, event(0, "a", "d1"), event(0, "b", "d1"), event(0, "b", "d2"), event(0, "c", "d2"), fraud(1, "c"))
	sg := g.Neighborhood(2*hour, "a", 2, 100)
	var ids []string
	for _, n := range sg.Nodes {
		ids = append(ids, n.Kind+":"+n.ID)
	}
	if got := fmt.Sprint(ids); got != "[card:a device:d1 card:b]" {
		t.Fatalf("2 hops from a: %s", got)
	}
	sg = g.Neighborhood(2*hour, "a", 4, 100)
	if len(sg.Nodes) != 5 || len(sg.Edges) != 4 || !sg.Nodes[len(sg.Nodes)-1].Fraud {
		t.Fatalf("4 hops from a: %+v", sg)
	}
	if sg = g.Neighborhood(2*hour, "a", 4, 2); !sg.Truncated || len(sg.Nodes) != 2 {
		t.Fatalf("max nodes not enforced: %+v", sg)
	}
}
