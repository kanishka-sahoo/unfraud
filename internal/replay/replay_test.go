package replay

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

type sliceSource struct {
	recs []profile.Record
	i    int
}

func (s *sliceSource) Next() (profile.Record, error) {
	if s.i == len(s.recs) {
		return profile.Record{}, io.EOF
	}
	s.i++
	return s.recs[s.i-1], nil
}

func (s *sliceSource) Close() error { return nil }

type memSink struct{ out []Out }

func (m *memSink) Send(_ context.Context, o Out) error { m.out = append(m.out, o); return nil }
func (m *memSink) Flush(context.Context) error         { return nil }

func event(id, key string, ms int64, l unfraudv1.Label) profile.Record {
	ev := &unfraudv1.Event{EventId: id, Entities: map[string]string{"k": key}}
	return profile.Record{Env: profile.NewEvent("p", ms, ev), Key: key, Label: l}
}

func link(src string, ms int64) profile.Record {
	env := profile.NewLink("p", ms, &unfraudv1.EntityRef{Type: "tx", Id: src}, &unfraudv1.EntityRef{Type: "tx", Id: "x"})
	return profile.Record{Env: env, Key: src}
}

// describe renders the stream compactly, e.g. "E:a@0 R:a@10 W@9".
func describe(out []Out) string {
	var b strings.Builder
	for i, o := range out {
		if i > 0 {
			b.WriteByte(' ')
		}
		t := o.Env.EventTimeMs
		switch p := o.Env.Payload.(type) {
		case *unfraudv1.Envelope_Event:
			b.WriteString("E:" + p.Event.EventId)
		case *unfraudv1.Envelope_Link:
			b.WriteString("L:" + p.Link.Src.Id)
		case *unfraudv1.Envelope_LabelRelease:
			b.WriteString("R:" + p.LabelRelease.EventId)
		case *unfraudv1.Envelope_Watermark:
			b.WriteString("W")
		}
		b.WriteString("@" + strconv.FormatInt(t, 10))
	}
	return b.String()
}

func replayAll(t *testing.T, recs []profile.Record, opt Options) ([]Out, Stats) {
	t.Helper()
	sink := &memSink{}
	stats, err := Run(context.Background(), "p", &sliceSource{recs: recs}, &sliceSource{recs: recs}, sink, opt)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(sink.out); i++ {
		if sink.out[i].Env.EventTimeMs < sink.out[i-1].Env.EventTimeMs {
			t.Fatalf("stream not in Event Time order at %d: %s", i, describe(sink.out))
		}
	}
	return sink.out, stats
}

const (
	fraud = unfraudv1.Label_LABEL_FRAUD
	legit = unfraudv1.Label_LABEL_LEGIT
	none  = unfraudv1.Label_LABEL_UNSPECIFIED
)

func TestLabelReleasesTrailEventsByDelay(t *testing.T) {
	recs := []profile.Record{
		event("a", "ka", 0, fraud),
		event("b", "kb", 3, none),
		event("c", "kc", 5, legit),
		event("d", "kd", 10, legit), // a's release (0+10) ties with d and goes first
		event("e", "ke", 12, fraud),
	}
	out, stats := replayAll(t, recs, Options{LabelDelay: 10 * time.Millisecond, WatermarkInterval: time.Hour})
	want := "E:a@0 E:b@3 E:c@5 R:a@10 E:d@10 E:e@12 R:c@15 R:d@20 R:e@22 W@22"
	if got := describe(out); got != want {
		t.Fatalf("stream\n got %s\nwant %s", got, want)
	}
	if stats.Events != 5 || stats.LabelReleases != 4 || stats.Watermarks != 1 {
		t.Fatalf("stats %+v", stats)
	}
	for _, o := range out {
		lr := o.Env.GetLabelRelease()
		if lr == nil {
			continue
		}
		if o.Key != "k"+lr.EventId {
			t.Fatalf("release for %s keyed %q, want the Event's key", lr.EventId, o.Key)
		}
		if lr.Entities["k"] != o.Key {
			t.Fatalf("release for %s carries entities %v, want the Event's", lr.EventId, lr.Entities)
		}
	}
}

func TestWatermarksCloseEachBucket(t *testing.T) {
	recs := []profile.Record{
		event("a", "ka", 1, none),
		event("b", "kb", 9, none),
		event("c", "kc", 10, none), // crosses into bucket 1
		link("c", 10),
		event("d", "kd", 35, none), // skips bucket 2: one watermark at its start
	}
	out, _ := replayAll(t, recs, Options{LabelDelay: time.Hour, WatermarkInterval: 10 * time.Millisecond})
	want := "E:a@1 E:b@9 W@9 E:c@10 L:c@10 W@29 E:d@35 W@35"
	if got := describe(out); got != want {
		t.Fatalf("stream\n got %s\nwant %s", got, want)
	}
	for _, o := range out {
		if o.Env.GetWatermark() != nil && o.Key != "" {
			t.Fatalf("watermark has key %q; it must go to every partition", o.Key)
		}
	}
}

func TestLimitOnlyReleasesLabelsOfSentEvents(t *testing.T) {
	recs := []profile.Record{
		event("a", "ka", 0, fraud),
		event("b", "kb", 1, fraud),
		event("c", "kc", 2, fraud),
	}
	out, _ := replayAll(t, recs, Options{LabelDelay: 5 * time.Millisecond, WatermarkInterval: time.Hour, Limit: 2})
	want := "E:a@0 E:b@1 R:a@5 R:b@6 W@6"
	if got := describe(out); got != want {
		t.Fatalf("stream\n got %s\nwant %s", got, want)
	}
}

func TestOutOfOrderDatasetFails(t *testing.T) {
	recs := []profile.Record{event("a", "ka", 5, none), event("b", "kb", 4, none)}
	_, err := Run(context.Background(), "p", &sliceSource{recs: recs}, &sliceSource{recs: recs}, &memSink{},
		Options{LabelDelay: time.Hour, WatermarkInterval: time.Hour})
	if err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("want out-of-order error, got %v", err)
	}
}

func TestPacingFollowsEventTimeAtSpeed(t *testing.T) {
	recs := []profile.Record{
		event("a", "ka", 0, fraud),
		event("b", "kb", 1000, none),
		event("c", "kc", 3000, none),
	}
	now := time.Unix(0, 0)
	var slept []time.Duration
	opt := Options{
		LabelDelay: time.Hour, WatermarkInterval: 24 * time.Hour, Speed: 10,
		now: func() time.Time { return now },
		sleep: func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			now = now.Add(d)
			return nil
		},
	}
	replayAll(t, recs, opt)
	// 1s and 3s of Event Time at 10x land at 100ms and 300ms; the trailing
	// label release is not paced.
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	if len(slept) != len(want) || slept[0] != want[0] || slept[1] != want[1] {
		t.Fatalf("slept %v, want %v", slept, want)
	}
}
