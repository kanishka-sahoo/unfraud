package tabformer

import (
	"io"
	"math"
	"testing"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

func readAll(t *testing.T, dir string) []profile.Record {
	t.Helper()
	src, err := Profile.Open(dir)
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

func num(ev *unfraudv1.Event, n string) float64 { i, _ := schema.NumIndex(n); return ev.Num[i] }
func cat(ev *unfraudv1.Event, n string) string  { i, _ := schema.CatIndex(n); return ev.Cat[i] }

func TestReadsPreparedEvents(t *testing.T) {
	recs := readAll(t, "testdata")
	if len(recs) != 3 {
		t.Fatalf("%d records, want 3", len(recs))
	}
	r := recs[0]
	ev := r.Env.GetEvent()
	if ev.EventId != "9973960" || r.Key != "841-2" || r.Label != unfraudv1.Label_LABEL_LEGIT {
		t.Fatalf("first record: id %q key %q label %v", ev.EventId, r.Key, r.Label)
	}
	if ev.Entities[Payee] != "1913477460590765860" || ev.Entities[Customer] != "841" {
		t.Fatalf("entities %v", ev.Entities)
	}
	if got := time.UnixMilli(r.Env.EventTimeMs).UTC(); !got.Equal(time.Date(2014, 10, 1, 0, 2, 0, 0, time.UTC)) {
		t.Fatalf("event time %v", got)
	}
	if num(ev, "amount_usd") != 79.73 || num(ev, "hour") != 0 || num(ev, "weekday") != 3 || num(ev, "fico_score") != 735 {
		t.Fatalf("numbers %v", ev.Num)
	}
	if cat(ev, "channel") != "in_person" || cat(ev, "mcc") != "5300" || cat(ev, "merchant_state") != "OH" {
		t.Fatalf("categories %v", ev.Cat)
	}

	// Empty cells are missing, and an empty customer is no Entity at all.
	r = recs[2]
	ev = r.Env.GetEvent()
	if r.Label != unfraudv1.Label_LABEL_FRAUD || cat(ev, "merchant_state") != "" || !math.IsNaN(num(ev, "abroad")) {
		t.Fatalf("third record: label %v num %v cat %v", r.Label, ev.Num, ev.Cat)
	}
	if _, ok := ev.Entities[Customer]; ok {
		t.Fatalf("empty customer became an Entity: %v", ev.Entities)
	}
}

func TestEveryFeatureIsDescribed(t *testing.T) {
	for _, n := range append(append([]string{}, schema.Num...), schema.Cat...) {
		if Profile.Glossary[n] == "" {
			t.Errorf("%s has no glossary entry, so Case Packets would call it anonymized", n)
		}
	}
	for _, f := range Profile.Features {
		if err := f.Validate(schema); err != nil {
			t.Error(err)
		}
	}
}

func TestWrongHeaderFails(t *testing.T) {
	if _, err := open("testdata/missing.csv"); err == nil {
		t.Fatal("a missing file must fail")
	}
	header[len(header)-1] = "renamed"
	defer func() { header[len(header)-1] = "fico_score" }()
	if _, err := open("testdata/events.csv"); err == nil {
		t.Fatal("a header that differs must fail")
	}
}
