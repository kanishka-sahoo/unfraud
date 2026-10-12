package ieeecis

import (
	"io"
	"maps"
	"math"
	"testing"
	"time"

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

func TestTransactionWithoutIdentity(t *testing.T) {
	rec := readAll(t)[0]
	ev := rec.Env.GetEvent()
	wantEntities := map[string]string{CardUID: "13926_315_-13", Addr: "315_87"}
	if !maps.Equal(ev.Entities, wantEntities) {
		t.Fatalf("entities %v, want %v", ev.Entities, wantEntities)
	}
	if rec.Key != "13926_315_-13" || rec.Label != unfraudv1.Label_LABEL_LEGIT || ev.EventId != "2987000" {
		t.Fatalf("key %q label %v id %q", rec.Key, rec.Label, ev.EventId)
	}
	if got := time.UnixMilli(rec.Env.EventTimeMs).UTC(); !got.Equal(time.Date(2017, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("event time %v", got)
	}
	wantNum := map[string]float64{"TransactionAmt": 68.5, "card1": 13926, "addr1": 315, "addr2": 87, "D1": 14, "V1": 1}
	if got := present(ev); !maps.Equal(got.num, wantNum) {
		t.Fatalf("num %v, want %v", got.num, wantNum)
	}
	wantCat := map[string]string{"ProductCD": "W", "card4": "discover", "M1": "T"}
	if got := present(ev); !maps.Equal(got.cat, wantCat) {
		t.Fatalf("cat %v, want %v", got.cat, wantCat)
	}
}

func TestTransactionJoinedWithIdentity(t *testing.T) {
	rec := readAll(t)[1]
	ev := rec.Env.GetEvent()
	wantEntities := map[string]string{
		CardUID: "2755_nan_nan",
		Device:  "SAMSUNG SM-G892A Build/NRD90M|Android 7.0|samsung browser 6.2|",
		EmailP:  "gmail.com",
		EmailR:  "gmail.com",
	}
	if !maps.Equal(ev.Entities, wantEntities) {
		t.Fatalf("entities %v, want %v", ev.Entities, wantEntities)
	}
	if rec.Label != unfraudv1.Label_LABEL_FRAUD {
		t.Fatalf("label %v", rec.Label)
	}
	got := present(ev)
	if got.num["id_01"] != -5 || got.cat["id_12"] != "NotFound" || got.cat["DeviceType"] != "mobile" {
		t.Fatalf("identity features not joined: %+v", got)
	}
}

func TestLabelAndIDsAreNotFeatures(t *testing.T) {
	for _, n := range []string{"isFraud", "TransactionID", "TransactionDT"} {
		_, num := schema.NumIndex(n)
		_, cat := schema.CatIndex(n)
		if num || cat {
			t.Errorf("%s must not be a feature", n)
		}
	}
	if len(schema.Num)+len(schema.Cat) != 391+40 {
		t.Errorf("schema has %d+%d features, want 391 transaction + 40 identity columns", len(schema.Num), len(schema.Cat))
	}
}

func TestUnknownColumnFails(t *testing.T) {
	if _, err := slots([]string{"TransactionID", "card1", "card7"}, "TransactionID"); err == nil {
		t.Fatal("a column outside the schema must fail")
	}
}

type features struct {
	num map[string]float64
	cat map[string]string
}

// present returns the non-missing features by name.
func present(ev *unfraudv1.Event) features {
	f := features{num: map[string]float64{}, cat: map[string]string{}}
	for i, v := range ev.Num {
		if !math.IsNaN(v) {
			f.num[schema.Num[i]] = v
		}
	}
	for i, v := range ev.Cat {
		if v != "" {
			f.cat[schema.Cat[i]] = v
		}
	}
	return f
}
