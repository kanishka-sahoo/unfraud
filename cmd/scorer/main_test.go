package main

import (
	"testing"

	"google.golang.org/protobuf/proto"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
)

func TestEventTimeReadsOnlyTheEnvelopeField(t *testing.T) {
	for _, env := range []*unfraudv1.Envelope{
		{Profile: "p", EventTimeMs: 1512345678901, Payload: &unfraudv1.Envelope_Event{Event: &unfraudv1.Event{
			EventId: "x", Num: []float64{1, 2}, Entities: map[string]string{"a": "b"}}}},
		{EventTimeMs: 7, Payload: &unfraudv1.Envelope_Watermark{Watermark: &unfraudv1.Watermark{}}},
		{Profile: "p"}, // zero time is omitted on the wire
	} {
		b, err := proto.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		got, err := eventTime(b)
		if err != nil || got != env.EventTimeMs {
			t.Errorf("eventTime = %d, %v; want %d", got, err, env.EventTimeMs)
		}
	}
	if _, err := eventTime([]byte{0x12, 0xff}); err == nil {
		t.Error("truncated record accepted")
	}
}
