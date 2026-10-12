// Command graphsvc is the single writer of a Profile's graph (ADR-0007). It
// reads every partition of the input topic in Event Time order, keeps exact
// in-memory feature and graph state, and writes Revised Decisions, made with
// graph (IEEE-CIS) or flow (Elliptic) features, to revised.<profile>.
//
// It recovers by recomputation: on start it reads the input topic from the
// beginning to rebuild state, and emits only for records after the offsets
// it committed last (ADR-0003).
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
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/align"
	"github.com/kanishka-sahoo/unfraud/internal/features"
	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
	"github.com/kanishka-sahoo/unfraud/internal/scoring"
)

var (
	revised = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_graphsvc_revised_total", Help: "Revised Decisions written, by action.",
	}, []string{"profile", "action"})
	replayed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_graphsvc_replayed_records_total", Help: "Records read only to rebuild state after a restart.",
	}, []string{"profile"})
	eventTimeLag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "unfraud_graphsvc_event_time_seconds", Help: "Event Time of the last record processed.",
	}, []string{"profile"})
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("graphsvc failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	var (
		profileName = flag.String("profile", "", "profile: "+strings.Join(registry.Names(), ", "))
		brokers     = flag.String("brokers", "localhost:9092", "comma-separated Kafka bootstrap servers")
		modelDir    = flag.String("model", "/model", "Revision model folder")
		group       = flag.String("group", "", "group that stores progress (default graph.<profile>)")
		listen      = flag.String("listen", ":8080", "address for /metrics and /healthz")
	)
	flag.Parse()
	p, err := registry.Get(*profileName)
	if err != nil {
		return err
	}
	if *group == "" {
		*group = "graph." + p.Name
	}
	in, out := "input."+p.Name, "revised."+p.Name
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	version, err := scoring.ModelVersion(*modelDir)
	if err != nil {
		return err
	}
	sc, err := scoring.New(p, features.NewMemory(), *modelDir, scoring.DefaultConfig,
		features.WithGraph(p), features.WithFlow(p))
	if err != nil {
		return err
	}

	seeds := strings.Split(*brokers, ",")
	admCl, err := kgo.NewClient(kgo.SeedBrokers(seeds...))
	if err != nil {
		return err
	}
	defer admCl.Close()
	adm := kadm.NewClient(admCl)
	committed, partitions, err := progress(ctx, adm, *group, in)
	if err != nil {
		return err
	}
	starts := map[int32]kgo.Offset{}
	for _, part := range partitions {
		starts[part] = kgo.NewOffset().AtStart()
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(seeds...),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{in: starts}),
		kgo.FetchMaxBytes(8<<20),
		kgo.DefaultProduceTopic(out),
		kgo.ProducerLinger(5*time.Millisecond),
		kgo.ProducerBatchCompression(kgo.ZstdCompression().WithLevel(1)),
	)
	if err != nil {
		return err
	}
	defer cl.Close()
	go serve(log, *listen)
	log.Info("graphsvc starting", "profile", p.Name, "input", in, "output", out, "model", version,
		"resume_offsets", committed)

	buf := align.NewBuffer()
	next := map[int32]int64{} // offset after the last record processed, per partition
	emit := func(d *scoring.Decision, ingested time.Time) error {
		b, err := proto.Marshal(scoring.Proto(p.Name, version, unfraudv1.Stage_STAGE_REVISED, d, ingested, time.Now()))
		if err != nil {
			return err
		}
		revised.WithLabelValues(p.Name, d.Action.String()).Inc()
		cl.Produce(ctx, &kgo.Record{Key: []byte(d.EventID), Value: b}, func(_ *kgo.Record, err error) {
			if err != nil {
				log.Error("produce failed", "event", d.EventID, "err", err)
			}
		})
		return nil
	}
	for {
		fetches := cl.PollFetches(ctx)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return nil
		}
		if err := fetches.Err0(); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		fetches.EachPartition(func(ftp kgo.FetchTopicPartition) {
			items := make([]align.Item, 0, len(ftp.Records))
			for _, r := range ftp.Records {
				env := &unfraudv1.Envelope{}
				if err := proto.Unmarshal(r.Value, env); err != nil {
					log.Error("undecodable record skipped", "partition", r.Partition, "offset", r.Offset, "err", err)
					continue
				}
				items = append(items, align.Item{Partition: r.Partition, Offset: r.Offset, TimeMs: env.EventTimeMs,
					Sequence: env.Sequence, Value: input{r, env}})
			}
			buf.Add(ftp.Partition, items, ftp.HighWatermark)
		})
		// Bound memory while a lagging partition holds the others back.
		var pause, resume []int32
		for _, part := range partitions {
			switch q := buf.Queued(part); {
			case q > 20000:
				pause = append(pause, part)
			case q < 5000:
				resume = append(resume, part)
			}
		}
		cl.PauseFetchPartitions(map[string][]int32{in: pause})
		cl.ResumeFetchPartitions(map[string][]int32{in: resume})
		for {
			chunk := buf.Next(1024)
			if len(chunk) == 0 {
				break
			}
			for _, it := range chunk {
				in := it.Value.(input)
				// Records before the committed offset only rebuild state.
				live := in.rec.Offset >= committed[in.rec.Partition]
				d, err := sc.Process(ctx, in.env)
				if err != nil {
					return fmt.Errorf("partition %d offset %d: %w", in.rec.Partition, in.rec.Offset, err)
				}
				closed := sc.Closed()
				sc.Revisions() // Ring Revisions are reported by case-svc's stream, not here
				next[in.rec.Partition] = in.rec.Offset + 1
				eventTimeLag.WithLabelValues(p.Name).Set(float64(in.env.EventTimeMs) / 1000)
				if !live {
					replayed.WithLabelValues(p.Name).Inc()
					continue
				}
				if d != nil {
					if err := emit(d, in.rec.Timestamp); err != nil {
						return err
					}
				}
				for _, c := range closed {
					if err := emit(c, in.rec.Timestamp); err != nil {
						return err
					}
				}
			}
		}
		if err := cl.Flush(ctx); err != nil {
			return err
		}
		if err := commit(ctx, adm, *group, in, next, committed); err != nil {
			return err
		}
	}
}

type input struct {
	rec *kgo.Record
	env *unfraudv1.Envelope
}

// progress returns the group's committed offsets (0 when none) and the
// topic's partitions.
func progress(ctx context.Context, adm *kadm.Client, group, topic string) (map[int32]int64, []int32, error) {
	topics, err := adm.ListTopics(ctx, topic)
	if err != nil {
		return nil, nil, err
	}
	td, ok := topics[topic]
	if !ok || td.Err != nil {
		return nil, nil, fmt.Errorf("topic %s: %v", topic, td.Err)
	}
	committed := map[int32]int64{}
	var parts []int32
	for p := range td.Partitions {
		parts = append(parts, p)
		committed[p] = 0
	}
	offs, err := adm.FetchOffsets(ctx, group)
	if err != nil && !errors.Is(err, kadm.ErrEmpty) {
		return nil, nil, err
	}
	offs.Each(func(o kadm.OffsetResponse) {
		if o.Topic == topic && o.Err == nil && o.At >= 0 {
			committed[o.Partition] = o.At
		}
	})
	return committed, parts, nil
}

// commit records progress beyond what was committed before.
func commit(ctx context.Context, adm *kadm.Client, group, topic string, next, committed map[int32]int64) error {
	offs := kadm.Offsets{}
	for p, o := range next {
		if o > committed[p] {
			offs.Add(kadm.Offset{Topic: topic, Partition: p, At: o, LeaderEpoch: -1})
			committed[p] = o
		}
	}
	if len(offs) == 0 {
		return nil
	}
	resp, err := adm.CommitOffsets(ctx, group, offs)
	if err != nil {
		return err
	}
	return resp.Error()
}

func serve(log *slog.Logger, addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Error("http server stopped", "err", err)
	}
}
