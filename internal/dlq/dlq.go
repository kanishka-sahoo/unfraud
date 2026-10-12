// Package dlq sends records a service cannot process to dlq.<profile>, with
// the error and the record's origin in headers, so one bad record never
// stops a consumer and nothing is silently dropped.
package dlq

import (
	"context"
	"strconv"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Topic is the dead-letter topic for a Profile.
func Topic(profile string) string { return "dlq." + profile }

// Send produces a copy of rec to the Profile's dead-letter topic.
func Send(ctx context.Context, cl *kgo.Client, profile, service string, rec *kgo.Record, cause error) {
	cl.Produce(ctx, &kgo.Record{
		Topic: Topic(profile),
		Key:   rec.Key,
		Value: rec.Value,
		Headers: []kgo.RecordHeader{
			{Key: "error", Value: []byte(cause.Error())},
			{Key: "service", Value: []byte(service)},
			{Key: "source.topic", Value: []byte(rec.Topic)},
			{Key: "source.partition", Value: []byte(strconv.Itoa(int(rec.Partition)))},
			{Key: "source.offset", Value: []byte(strconv.FormatInt(rec.Offset, 10))},
		},
	}, nil)
}
