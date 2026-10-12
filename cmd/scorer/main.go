// Command scorer consumes a Profile's input topic, scores every Event with
// real-time features (shared in Valkey), the model and Rules, and writes
// Decisions to scored.<profile>. Records are released in Event Time order
// across partitions (ADR-0016) and scored in small chunks, partition-parallel
// within a chunk; offsets are committed only after Decisions are flushed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/valkey-io/valkey-go"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/align"
	"github.com/kanishka-sahoo/unfraud/internal/dlq"
	"github.com/kanishka-sahoo/unfraud/internal/features"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
	"github.com/kanishka-sahoo/unfraud/internal/scoring"
)

var (
	decisions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_scorer_decisions_total", Help: "Decisions by action.",
	}, []string{"profile", "action"})
	records = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_scorer_records_total", Help: "Input records processed by type.",
	}, []string{"profile", "type"})
	processSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "unfraud_scorer_process_seconds", Help: "Time to score one Event inside the scorer.",
		Buckets: prometheus.ExponentialBuckets(0.0001, 2, 16),
	}, []string{"profile"})
	degradedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_scorer_degraded_total", Help: "Events scored without real-time features (state unreachable).",
	}, []string{"profile"})
	deadLettered = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_scorer_dead_lettered_total", Help: "Records sent to the dead-letter topic.",
	}, []string{"profile"})
	shadowAgreement = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_scorer_shadow_total", Help: "Shadow Model decisions by active and shadow model action.",
	}, []string{"profile", "active", "shadow"})
	endToEndSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "unfraud_scorer_end_to_end_seconds", Help: "Input produced to Decision made (wall clock).",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 16),
	}, []string{"profile"})
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("scorer failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	var (
		profileName = flag.String("profile", "", "profile: "+strings.Join(registry.Names(), ", "))
		brokers     = flag.String("brokers", "localhost:9092", "comma-separated Kafka bootstrap servers")
		valkeyAddr  = flag.String("valkey", "localhost:6379", "Valkey address for feature state")
		modelDir    = flag.String("model", "/model", "model folder (model.txt, manifest.json)")
		group       = flag.String("group", "", "consumer group (default scorer.<profile>)")
		listen      = flag.String("listen", ":8080", "address for /metrics, /healthz and /readyz")
		aligned     = flag.Bool("align", true, "align partitions in Event Time (false only to measure its effect)")
		shadowDir   = flag.String("shadow-model", "", "Shadow Model folder, scored alongside but never acted on")
	)
	flag.Parse()
	p, err := registry.Get(*profileName)
	if err != nil {
		return err
	}
	if *group == "" {
		*group = "scorer." + p.Name
	}
	in, out := "input."+p.Name, "scored."+p.Name

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	version, err := scoring.ModelVersion(*modelDir)
	if err != nil {
		return err
	}
	vk, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{*valkeyAddr}})
	if err != nil {
		return fmt.Errorf("valkey: %w", err)
	}
	defer vk.Close()
	state := features.NewValkey(vk, p.Name+":")

	buf := align.NewBuffer()
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(*brokers, ",")...),
		kgo.ConsumerGroup(*group),
		kgo.ConsumeTopics(in),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		// Offsets of scored records are marked after their Decisions are
		// flushed and committed in the background; a crash redelivers at
		// most about a second of records, which the feature Guards absorb.
		kgo.AutoCommitMarks(),
		kgo.AutoCommitInterval(time.Second),
		kgo.BlockRebalanceOnPoll(),
		kgo.OnPartitionsRevoked(func(_ context.Context, _ *kgo.Client, lost map[string][]int32) {
			for _, ps := range lost {
				for _, p := range ps {
					buf.Drop(p)
				}
			}
		}),
		kgo.OnPartitionsLost(func(_ context.Context, _ *kgo.Client, lost map[string][]int32) {
			for _, ps := range lost {
				for _, p := range ps {
					buf.Drop(p)
				}
			}
		}),
		// Bound fetch buffering to fit the pod's memory limit.
		kgo.FetchMaxBytes(8<<20),
		kgo.FetchMaxPartitionBytes(1<<20),
		kgo.DefaultProduceTopic(out),
		kgo.ProducerLinger(2*time.Millisecond),
		kgo.ProducerBatchCompression(kgo.ZstdCompression().WithLevel(1)),
	)
	if err != nil {
		return err
	}
	defer cl.Close()

	// Ready means both dependencies answer, not that data has arrived: a
	// caught-up scorer with nothing to consume is still ready.
	readyCh := make(chan struct{})
	go serve(log, *listen, readyCh)
	if err := cl.Ping(ctx); err != nil {
		return fmt.Errorf("kafka: %w", err)
	}
	if err := vk.Do(ctx, vk.B().Ping().Build()).Error(); err != nil {
		return fmt.Errorf("valkey: %w", err)
	}
	close(readyCh)

	cfg := scoring.DefaultConfig
	cfg.ShadowDir = *shadowDir
	s := &service{
		log: log, p: p, cl: cl, state: state, modelDir: *modelDir, version: version, cfg: cfg,
		workers: map[int32]*scoring.Scorer{},
	}
	log.Info("scorer starting", "profile", p.Name, "input", in, "output", out, "group", *group, "model", version)
	for {
		fetches := cl.PollFetches(ctx)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			final, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return cl.CommitMarkedOffsets(final)
		}
		if err := fetches.Err0(); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		fetches.EachError(func(t string, p int32, err error) {
			log.Warn("fetch error", "topic", t, "partition", p, "err", err)
		})
		var fetched []int32
		fetches.EachPartition(func(ftp kgo.FetchTopicPartition) {
			// Only the Event Time is read now; full decoding waits until the
			// record is scored, so buffered records stay small.
			items := make([]align.Item, 0, len(ftp.Records))
			for _, r := range ftp.Records {
				t, seq, err := ordering(r.Value)
				if err != nil {
					s.deadLetter(ctx, r, err)
					continue
				}
				items = append(items, align.Item{Partition: r.Partition, Offset: r.Offset, TimeMs: t, Sequence: seq, Value: r})
			}
			buf.Add(ftp.Partition, items, ftp.HighWatermark)
			fetched = append(fetched, ftp.Partition)
		})

		var done []*kgo.Record
		for {
			var chunk []align.Item
			if *aligned {
				chunk = buf.Next(chunkSize)
			} else {
				chunk = buf.Drain()
			}
			if len(chunk) == 0 {
				break
			}
			if err := s.process(ctx, chunk); err != nil {
				return err
			}
			for _, it := range chunk {
				done = append(done, it.Value.(*kgo.Record))
			}
		}
		if err := cl.Flush(ctx); err != nil {
			return err
		}
		cl.MarkCommitRecords(done...)
		// Bound memory: stop fetching partitions that already have plenty
		// buffered, usually because another partition is holding them back.
		var pause, resume []int32
		for _, p := range fetched {
			if buf.Queued(p) > maxQueued {
				pause = append(pause, p)
			}
		}
		for _, p := range cl.PauseFetchPartitions(nil)[in] {
			if buf.Queued(p) <= maxQueued/2 {
				resume = append(resume, p)
			}
		}
		cl.PauseFetchPartitions(map[string][]int32{in: pause})
		cl.ResumeFetchPartitions(map[string][]int32{in: resume})
		cl.AllowRebalance()
	}
}

const (
	// chunkSize bounds how far partitions may drift apart inside the scorer:
	// a chunk's records run partition-parallel, so only Events within one
	// chunk can see each other out of Event Time order.
	chunkSize = 64
	maxQueued = 2000
)

// ordering reads Envelope.event_time_ms (field 2) and sequence (field 3)
// without decoding the rest.
func ordering(b []byte) (timeMs, seq int64, err error) {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return 0, 0, protowire.ParseError(n)
		}
		b = b[n:]
		if (num == 2 || num == 3) && typ == protowire.VarintType {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return 0, 0, protowire.ParseError(n)
			}
			b = b[n:]
			if num == 2 {
				timeMs = int64(v)
			} else {
				seq = int64(v)
			}
			continue
		}
		n = protowire.ConsumeFieldValue(num, typ, b)
		if n < 0 {
			return 0, 0, protowire.ParseError(n)
		}
		b = b[n:]
	}
	return timeMs, seq, nil
}

type service struct {
	log      *slog.Logger
	p        profile.Profile
	cl       *kgo.Client
	state    features.State
	modelDir string
	version  string
	cfg      scoring.Config

	mu      sync.Mutex
	workers map[int32]*scoring.Scorer
}

func (s *service) worker(partition int32) (*scoring.Scorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.workers[partition]; ok {
		return w, nil
	}
	w, err := scoring.New(s.p, s.state, s.modelDir, s.cfg)
	if err != nil {
		return nil, err
	}
	s.workers[partition] = w
	return w, nil
}

// process scores a chunk: each partition's records in order, partitions in
// parallel.
func (s *service) process(ctx context.Context, chunk []align.Item) error {
	byPartition := map[int32][]*kgo.Record{}
	for _, it := range chunk {
		byPartition[it.Partition] = append(byPartition[it.Partition], it.Value.(*kgo.Record))
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(byPartition))
	for p, ins := range byPartition {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.partition(ctx, p, ins); err != nil {
				errs <- fmt.Errorf("partition %d: %w", p, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	return <-errs
}

func (s *service) partition(ctx context.Context, partition int32, recs []*kgo.Record) error {
	sc, err := s.worker(partition)
	if err != nil {
		return err
	}
	for _, r := range recs {
		env := &unfraudv1.Envelope{}
		if err := proto.Unmarshal(r.Value, env); err != nil {
			s.deadLetter(ctx, r, err)
			continue
		}
		start := time.Now()
		d, err := sc.Process(ctx, env)
		if err != nil {
			s.deadLetter(ctx, r, err)
			continue
		}
		records.WithLabelValues(s.p.Name, kind(env)).Inc()
		if d == nil {
			continue
		}
		now := time.Now()
		processSeconds.WithLabelValues(s.p.Name).Observe(now.Sub(start).Seconds())
		endToEndSeconds.WithLabelValues(s.p.Name).Observe(now.Sub(r.Timestamp).Seconds())
		decisions.WithLabelValues(s.p.Name, d.Action.String()).Inc()
		if d.Degraded {
			degradedTotal.WithLabelValues(s.p.Name).Inc()
		}
		if d.Shadow != nil {
			shadowAgreement.WithLabelValues(s.p.Name, d.ModelAction.String(), d.Shadow.Action.String()).Inc()
		}

		b, err := proto.Marshal(scoring.Proto(s.p.Name, s.version, unfraudv1.Stage_STAGE_PROVISIONAL, d, r.Timestamp, now))
		if err != nil {
			return err
		}
		s.cl.Produce(ctx, &kgo.Record{Key: r.Key, Value: b}, func(_ *kgo.Record, err error) {
			if err != nil {
				s.log.Error("produce failed", "event", d.EventID, "err", err)
			}
		})
	}
	return nil
}

// deadLetter parks a record the scorer cannot process and moves on.
func (s *service) deadLetter(ctx context.Context, r *kgo.Record, err error) {
	s.log.Error("record dead-lettered", "partition", r.Partition, "offset", r.Offset, "err", err)
	deadLettered.WithLabelValues(s.p.Name).Inc()
	dlq.Send(ctx, s.cl, s.p.Name, "scorer", r, err)
}

func kind(env *unfraudv1.Envelope) string {
	switch env.Payload.(type) {
	case *unfraudv1.Envelope_Event:
		return "event"
	case *unfraudv1.Envelope_Link:
		return "link"
	case *unfraudv1.Envelope_Watermark:
		return "watermark"
	case *unfraudv1.Envelope_LabelRelease:
		return "label_release"
	}
	return "unknown"
}

func serve(log *slog.Logger, addr string, ready <-chan struct{}) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-ready:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Error("http server stopped", "err", err)
	}
}
