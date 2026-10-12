package replay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var marshal = proto.MarshalOptions{Deterministic: true}

// KafkaSink writes Envelopes as Protobuf to one topic. Keyed records use
// Kafka's default murmur2 partitioning; Watermarks are copied to every
// partition. Record timestamps are left as produce time: setting them to
// Event Time (years in the past) would let time-based retention delete them.
type KafkaSink struct {
	cl         *kgo.Client
	topic      string
	partitions int

	mu  sync.Mutex
	err error
}

type KafkaConfig struct {
	Brokers []string
	Topic   string
	// CreatePartitions, if positive, creates the topic when it is missing.
	// In the cluster, topics are declared as KafkaTopic resources instead.
	CreatePartitions int
}

func NewKafkaSink(ctx context.Context, cfg KafkaConfig) (*KafkaSink, error) {
	kafkaKey := kgo.StickyKeyPartitioner(nil)
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.DefaultProduceTopic(cfg.Topic),
		// Replays are throughput-bound: small lingers fill batches, and the
		// fastest zstd level keeps compression off the critical path.
		kgo.ProducerBatchCompression(kgo.ZstdCompression().WithLevel(1)),
		kgo.ProducerLinger(5*time.Millisecond),
		kgo.RecordPartitioner(kgo.BasicConsistentPartitioner(func(topic string) func(*kgo.Record, int) int {
			keyed := kafkaKey.ForTopic(topic)
			return func(r *kgo.Record, n int) int {
				if r.Key == nil {
					return int(r.Partition) // a Watermark copy, pinned by Send
				}
				return keyed.Partition(r, n)
			}
		})),
	)
	if err != nil {
		return nil, err
	}
	n, err := topicPartitions(ctx, kadm.NewClient(cl), cfg)
	if err != nil {
		cl.Close()
		return nil, err
	}
	return &KafkaSink{cl: cl, topic: cfg.Topic, partitions: n}, nil
}

func topicPartitions(ctx context.Context, adm *kadm.Client, cfg KafkaConfig) (int, error) {
	if cfg.CreatePartitions > 0 {
		_, err := adm.CreateTopic(ctx, int32(cfg.CreatePartitions), -1, nil, cfg.Topic)
		if err != nil && !errors.Is(err, kerr.TopicAlreadyExists) {
			return 0, fmt.Errorf("creating topic %s: %w", cfg.Topic, err)
		}
	}
	topics, err := adm.ListTopics(ctx, cfg.Topic)
	if err != nil {
		return 0, fmt.Errorf("describing topic %s: %w", cfg.Topic, err)
	}
	td, ok := topics[cfg.Topic]
	if !ok || td.Err != nil {
		return 0, fmt.Errorf("topic %s not found: %v", cfg.Topic, td.Err)
	}
	return len(td.Partitions), nil
}

// Partitions reports the topic's partition count.
func (s *KafkaSink) Partitions() int { return s.partitions }

func (s *KafkaSink) Send(ctx context.Context, o Out) error {
	if err := s.firstErr(); err != nil {
		return err
	}
	b, err := marshal.Marshal(o.Env)
	if err != nil {
		return err
	}
	if o.Key == "" {
		for p := 0; p < s.partitions; p++ {
			s.cl.Produce(ctx, &kgo.Record{Partition: int32(p), Value: b}, s.done)
		}
		return nil
	}
	s.cl.Produce(ctx, &kgo.Record{Key: []byte(o.Key), Value: b}, s.done)
	return nil
}

func (s *KafkaSink) done(_ *kgo.Record, err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *KafkaSink) firstErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *KafkaSink) Flush(ctx context.Context) error {
	if err := s.cl.Flush(ctx); err != nil {
		return err
	}
	return s.firstErr()
}

func (s *KafkaSink) Close() { s.cl.Close() }

// JSONSink writes one protojson Envelope per line, for inspection.
type JSONSink struct{ w *bufio.Writer }

func NewJSONSink(w io.Writer) *JSONSink { return &JSONSink{w: bufio.NewWriter(w)} }

func (s *JSONSink) Send(_ context.Context, o Out) error {
	b, err := protojson.Marshal(o.Env)
	if err != nil {
		return err
	}
	if _, err := s.w.Write(b); err != nil {
		return err
	}
	return s.w.WriteByte('\n')
}

func (s *JSONSink) Flush(context.Context) error { return s.w.Flush() }

// DiscardSink drops everything; Run's Stats still count it.
type DiscardSink struct{}

func (DiscardSink) Send(context.Context, Out) error { return nil }
func (DiscardSink) Flush(context.Context) error     { return nil }
