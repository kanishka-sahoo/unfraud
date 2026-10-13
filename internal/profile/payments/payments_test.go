package payments

import (
	"io"
	"math"
	"testing"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

func readAll(t *testing.T, p profile.Profile, dir string) []profile.Record {
	t.Helper()
	src, err := p.Open(dir)
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

func num(t *testing.T, p profile.Profile, ev *unfraudv1.Event, name string) float64 {
	t.Helper()
	i, ok := p.Schema.NumIndex(name)
	if !ok {
		t.Fatalf("no numeric feature %s", name)
	}
	return ev.Num[i]
}

func cat(t *testing.T, p profile.Profile, ev *unfraudv1.Event, name string) string {
	t.Helper()
	i, ok := p.Schema.CatIndex(name)
	if !ok {
		t.Fatalf("no categorical feature %s", name)
	}
	return ev.Cat[i]
}

func TestReadsPaymentSchema(t *testing.T) {
	recs := readAll(t, Sparkov, "testdata/sparkov")
	if len(recs) != 3 {
		t.Fatalf("%d records, want 3", len(recs))
	}
	r := recs[0]
	ev := r.Env.GetEvent()
	if ev.EventId != "0b242abb623afc578575680df30655b9" || r.Key != "2703186189652095" || r.Label != unfraudv1.Label_LABEL_LEGIT {
		t.Fatalf("first record: id %q key %q label %v", ev.EventId, r.Key, r.Label)
	}
	if ev.Entities[Payee] != "Rippin, Kub and Mann" || ev.Entities[Customer] != "2703186189652095" {
		t.Fatalf("entities %v", ev.Entities)
	}
	if got := time.UnixMilli(r.Env.EventTimeMs).UTC(); !got.Equal(time.Date(2018, 12, 31, 0, 0, 18, 0, time.UTC)) {
		t.Fatalf("event time %v", got)
	}
	if num(t, Sparkov, ev, "amount_usd") != 4.97 || num(t, Sparkov, ev, "hour") != 0 || num(t, Sparkov, ev, "weekday") != 1 {
		t.Fatalf("core numbers %v", ev.Num)
	}
	if cat(t, Sparkov, ev, "channel") != "online" || cat(t, Sparkov, ev, "job") != "Psychologist, counselling" {
		t.Fatalf("categories %v", ev.Cat)
	}

	// Empty cells are missing, and an empty customer is no Entity at all.
	ev = recs[2].Env.GetEvent()
	if recs[2].Label != unfraudv1.Label_LABEL_FRAUD || cat(t, Sparkov, ev, "channel") != "" ||
		!math.IsNaN(num(t, Sparkov, ev, "merchant_km")) {
		t.Fatalf("third record: label %v num %v cat %v", recs[2].Label, ev.Num, ev.Cat)
	}
	if _, ok := ev.Entities[Customer]; ok {
		t.Fatalf("empty customer became an Entity: %v", ev.Entities)
	}
}

func TestEveryPaymentProfileSharesTheCore(t *testing.T) {
	for _, p := range []profile.Profile{TabFormer, Sparkov, AMLWorld} {
		for i, n := range CoreNum {
			if p.Schema.Num[i] != n {
				t.Errorf("%s: numeric feature %d is %s, want %s", p.Name, i, p.Schema.Num[i], n)
			}
		}
		for i, n := range CoreCat {
			if p.Schema.Cat[i] != n {
				t.Errorf("%s: categorical feature %d is %s, want %s", p.Name, i, p.Schema.Cat[i], n)
			}
		}
		for _, f := range p.Features {
			if err := f.Validate(CoreSchema); err != nil {
				t.Errorf("%s: %v", p.Name, err)
			}
		}
		for _, n := range append(append([]string{}, p.Schema.Num...), p.Schema.Cat...) {
			if p.Glossary[n] == "" {
				t.Errorf("%s: %s has no glossary entry, so Case Packets would call it anonymized", p.Name, n)
			}
		}
		if !p.Split.Train.Before(p.Split.Validation) || !p.Split.Validation.Before(p.Split.Test) {
			t.Errorf("%s: split out of order: %+v", p.Name, p.Split)
		}
	}
}

func TestHeaderMismatchFails(t *testing.T) {
	if _, err := TabFormer.Open("testdata/sparkov"); err == nil {
		t.Fatal("a file with another Profile's extras must not open")
	}
}
