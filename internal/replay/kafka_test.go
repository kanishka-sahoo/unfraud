package replay

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

// TestKafkaSink runs against a real broker: UNFRAUD_KAFKA_BROKERS=localhost:30092
// with the kind cluster up (make cluster-up deploy).
func TestKafkaSink(t *testing.T) {
	brokers := os.Getenv("UNFRAUD_KAFKA_BROKERS")
	if brokers == "" {
		t.Skip("UNFRAUD_KAFKA_BROKERS not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	topic := fmt.Sprintf("test.sink.%d", time.Now().UnixNano())

	sink, err := NewKafkaSink(ctx, KafkaConfig{Brokers: strings.Split(brokers, ","), Topic: topic, CreatePartitions: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	defer func() {
		adm := kadm.NewClient(sink.cl)
		adm.DeleteTopics(context.Background(), topic)
	}()

	var recs []profile.Record
	for i := range 30 {
		recs = append(recs, event(fmt.Sprint(i), fmt.Sprintf("card-%d", i%7), int64(i), fraud))
	}
	stats, err := Run(ctx, "p", &sliceSource{recs: recs}, &sliceSource{recs: recs}, sink,
		Options{LabelDelay: 5 * time.Millisecond, WatermarkInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	total := stats.Events + stats.LabelReleases + stats.Watermarks*3

	cl, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(brokers, ",")...), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	byPartition := map[int32][]*unfraudv1.Envelope{}
	keyPartition := map[string]int32{}
	for n := 0; n < total; {
		fs := cl.PollFetches(ctx)
		if err := fs.Err0(); err != nil {
			t.Fatalf("after %d of %d records: %v", n, total, err)
		}
		fs.EachRecord(func(r *kgo.Record) {
			n++
			env := &unfraudv1.Envelope{}
			if err := proto.Unmarshal(r.Value, env); err != nil {
				t.Fatal(err)
			}
			byPartition[r.Partition] = append(byPartition[r.Partition], env)
			if len(r.Key) > 0 {
				if p, ok := keyPartition[string(r.Key)]; ok && p != r.Partition {
					t.Errorf("key %s on partitions %d and %d", r.Key, p, r.Partition)
				}
				keyPartition[string(r.Key)] = r.Partition
			}
		})
	}

	for p := range int32(3) {
		envs := byPartition[p]
		watermarks := 0
		for i, env := range envs {
			if env.GetWatermark() != nil {
				watermarks++
			}
			if i > 0 && env.EventTimeMs < envs[i-1].EventTimeMs {
				t.Errorf("partition %d out of Event Time order at %d", p, i)
			}
		}
		if watermarks != stats.Watermarks {
			t.Errorf("partition %d has %d watermarks, want all %d", p, watermarks, stats.Watermarks)
		}
	}
}
