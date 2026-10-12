package paysim

import (
	"io"
	"testing"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
)

func TestReadsEventsWithoutLeakyBalances(t *testing.T) {
	src, err := Profile.Open("testdata")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	rec, err := src.Next()
	if err != nil {
		t.Fatal(err)
	}
	ev := rec.Env.GetEvent()
	if ev.EventId != "1" || rec.Key != "C1231006815" || rec.Label != unfraudv1.Label_LABEL_LEGIT {
		t.Fatalf("first record: id %q key %q label %v", ev.EventId, rec.Key, rec.Label)
	}
	if ev.Entities["dest"] != "M1979787155" || ev.Num[0] != 9839.64 || ev.Cat[0] != "PAYMENT" || ev.SchemaId != schema.ID {
		t.Fatalf("first event: %v", ev)
	}
	if len(schema.Num) != 1 {
		t.Fatalf("only amount may be numeric (balances give away the label), got %v", schema.Num)
	}
	if got := time.UnixMilli(rec.Env.EventTimeMs).UTC(); !got.Equal(base.Add(time.Hour)) {
		t.Fatalf("step 1 at %v", got)
	}

	rec, err = src.Next()
	if err != nil {
		t.Fatal(err)
	}
	if rec.Label != unfraudv1.Label_LABEL_FRAUD || rec.Env.EventTimeMs != base.Add(3*time.Hour).UnixMilli() {
		t.Fatalf("second record: label %v time %d", rec.Label, rec.Env.EventTimeMs)
	}
	if _, err := src.Next(); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}
