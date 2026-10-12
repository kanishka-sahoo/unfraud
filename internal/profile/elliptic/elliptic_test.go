package elliptic

import (
	"io"
	"strings"
	"testing"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

func readAll(t *testing.T) []profile.Record {
	t.Helper()
	src, err := Profile.Open("testdata")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	var recs []profile.Record
	for {
		rec, err := src.Next()
		if err == io.EOF {
			return recs
		}
		if err != nil {
			t.Fatal(err)
		}
		recs = append(recs, rec)
	}
}

func TestEmitsEachTimestepsLinksAfterItsEvents(t *testing.T) {
	var got []string
	for _, r := range readAll(t) {
		if ev := r.Env.GetEvent(); ev != nil {
			got = append(got, "E"+ev.EventId)
		} else {
			l := r.Env.GetLink()
			got = append(got, "L"+l.Src.Id+">"+l.Dst.Id)
		}
	}
	want := "E10 E11 L10>11 L11>10 E20 E21 L20>21"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %s, want %s", strings.Join(got, " "), want)
	}
}

func TestEventsCarryLabelsTimeAndFeatures(t *testing.T) {
	recs := readAll(t)
	first, third := recs[0], recs[4]
	if first.Label != unfraudv1.Label_LABEL_FRAUD || recs[1].Label != unfraudv1.Label_LABEL_UNSPECIFIED ||
		third.Label != unfraudv1.Label_LABEL_LEGIT {
		t.Fatalf("labels: %v %v %v", first.Label, recs[1].Label, third.Label)
	}
	if d := third.Env.EventTimeMs - first.Env.EventTimeMs; d != stepDuration.Milliseconds() {
		t.Fatalf("timesteps %dms apart, want %d", d, stepDuration.Milliseconds())
	}
	if first.Env.EventTimeMs%stepDuration.Milliseconds() != 0 {
		t.Fatal("timesteps must align to watermark buckets")
	}
	ev := first.Env.GetEvent()
	num := func(n string) float64 { i, _ := schema.NumIndex(n); return ev.Num[i] }
	if len(ev.Num) != 1+93+72 || num("timestep") != 1 || num("local_93") != 0.5 || num("agg_1") != -0.25 {
		t.Fatalf("features: %d, timestep %v local_93 %v agg_1 %v", len(ev.Num), num("timestep"), num("local_93"), num("agg_1"))
	}
	if ev.Entities[entityType] != "10" || first.Key != "10" {
		t.Fatalf("entity %v key %q", ev.Entities, first.Key)
	}
}
