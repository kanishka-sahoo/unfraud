// Command casesvc turns Decisions into Cases. It consumes scored.<profile>
// (Provisional) and revised.<profile> (Revised), stores every Decision, opens
// a Case for each flagged Event (keeping the stricter of the two Stages), and
// computes Contributions (TreeSHAP) with the model that flagged it.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/dlq"
	"github.com/kanishka-sahoo/unfraud/internal/model"
	"github.com/kanishka-sahoo/unfraud/internal/scoring"
	"github.com/kanishka-sahoo/unfraud/internal/store"
)

// topContributions is how many features a Case keeps individually.
const topContributions = 12

var (
	decisionsStored = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_casesvc_decisions_total", Help: "Decisions consumed, by stage.",
	}, []string{"profile", "stage"})
	casesExplained = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_casesvc_contributions_total", Help: "Cases whose Contributions were computed.",
	}, []string{"profile"})
	unexplained = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_casesvc_unexplained_total", Help: "Cases left without Contributions (unknown model version or no input).",
	}, []string{"profile"})
	batchSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "unfraud_casesvc_batch_seconds", Help: "Time to store and explain one poll's Decisions.",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 14),
	}, []string{"profile"})
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("casesvc failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	var (
		profile = flag.String("profile", "", "profile")
		brokers = flag.String("brokers", "localhost:9092", "comma-separated Kafka bootstrap servers")
		models  = flag.String("models", "/models", "folder of model folders, one per model the scorer and graph-svc run")
		listen  = flag.String("listen", ":8080", "address for /metrics and /healthz")
	)
	flag.Parse()
	pgURL := os.Getenv("PG_URL")
	if *profile == "" || pgURL == "" {
		return errors.New("-profile and $PG_URL are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	byVersion, err := loadModels(*models)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, pgURL)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer st.Close()

	cl, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(*brokers, ",")...),
		kgo.ConsumerGroup("case."+*profile),
		kgo.ConsumeTopics("scored."+*profile, "revised."+*profile),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		// Bounded so a backlog of Decisions fits the pod's memory limit.
		kgo.FetchMaxBytes(4<<20),
		kgo.FetchMaxPartitionBytes(1<<20),
		kgo.MaxConcurrentFetches(1),
	)
	if err != nil {
		return err
	}
	defer cl.Close()
	go serve(log, *listen)
	versions := make([]string, 0, len(byVersion))
	for v := range byVersion {
		versions = append(versions, v)
	}
	log.Info("casesvc starting", "profile", *profile, "models", versions)

	for {
		// Bounded batches keep memory flat while a backlog drains.
		fetches := cl.PollRecords(ctx, 2000)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return nil
		}
		if err := fetches.Err0(); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		var batch []*unfraudv1.Scored
		fetches.EachRecord(func(r *kgo.Record) {
			s := &unfraudv1.Scored{}
			if err := proto.Unmarshal(r.Value, s); err != nil {
				log.Error("decision dead-lettered", "topic", r.Topic, "offset", r.Offset, "err", err)
				dlq.Send(ctx, cl, *profile, "casesvc", r, err)
				return
			}
			batch = append(batch, s)
		})
		if len(batch) == 0 {
			continue
		}
		start := time.Now()
		if err := handle(ctx, st, byVersion, *profile, batch); err != nil {
			return err
		}
		batchSeconds.WithLabelValues(*profile).Observe(time.Since(start).Seconds())
		if err := cl.CommitUncommittedOffsets(ctx); err != nil {
			return err
		}
	}
}

func handle(ctx context.Context, st *store.Store, byVersion map[string]*model.Model, profile string, batch []*unfraudv1.Scored) error {
	ds := make([]store.Decision, len(batch))
	for i, s := range batch {
		ds[i] = store.Decision{
			Profile: s.Profile, EventID: s.EventId, Stage: int16(s.Stage), EventTime: time.UnixMilli(s.EventTimeMs),
			Probability: float32(s.Probability), ModelAction: action(s.ModelAction), RuleAction: action(s.RuleAction),
			Action: action(s.Action), ReasonCodes: s.ReasonCodes, ModelVersion: s.ModelVersion,
			ScoredAt: time.UnixMilli(s.ScoredAtMs), IngestedAt: time.UnixMilli(s.IngestedAtMs), Entities: s.Entities,
		}
		decisionsStored.WithLabelValues(profile, s.Stage.String()).Inc()
	}
	fresh, err := st.InsertDecisions(ctx, ds)
	if err != nil {
		return fmt.Errorf("storing decisions: %w", err)
	}
	// Redelivered and replayed Decisions never change Cases.
	need, err := st.UpsertCases(ctx, fresh)
	if err != nil {
		return fmt.Errorf("upserting cases: %w", err)
	}
	// Explain each Case with the Decision UpsertCases recorded: the
	// strictest new one in this batch, the earlier Stage on ties.
	type stageKey struct {
		store.CaseKey
		stage int16
	}
	isFresh := map[stageKey]bool{}
	for _, d := range fresh {
		isFresh[stageKey{store.CaseKey{Profile: d.Profile, EventID: d.EventID}, d.Stage}] = true
	}
	strictest := map[store.CaseKey]*unfraudv1.Scored{}
	for _, s := range batch {
		k := store.CaseKey{Profile: s.Profile, EventID: s.EventId}
		sk := stageKey{k, int16(s.Stage)}
		if !isFresh[sk] {
			continue
		}
		delete(isFresh, sk) // the first copy is the one stored
		if prev, ok := strictest[k]; !ok || s.Action > prev.Action || s.Action == prev.Action && s.Stage < prev.Stage {
			strictest[k] = s
		}
	}
	for _, k := range need {
		s := strictest[k]
		m := byVersion[s.GetModelVersion()]
		if s == nil || m == nil || len(s.ModelInput) != len(m.Features) {
			unexplained.WithLabelValues(profile).Inc()
			continue
		}
		if err := st.SetContributions(ctx, k, explain(m, s)); err != nil {
			return fmt.Errorf("storing contributions: %w", err)
		}
		casesExplained.WithLabelValues(profile).Inc()
	}
	return nil
}

func explain(m *model.Model, s *unfraudv1.Scored) store.CaseContributions {
	bias, shap, values := m.Explain(s.ModelInput)
	idx := make([]int, len(shap))
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(a, b int) int {
		return cmp.Or(cmp.Compare(math.Abs(shap[b]), math.Abs(shap[a])), cmp.Compare(a, b))
	})
	c := store.CaseContributions{ModelVersion: s.ModelVersion, Stage: int16(s.Stage), Bias: bias, RawScore: s.RawScore}
	for rank, i := range idx {
		if rank < topContributions {
			c.Top = append(c.Top, store.Contribution{Feature: m.Features[i], Value: values[i], SHAP: shap[i]})
		} else {
			c.Other += shap[i]
		}
	}
	return c
}

// action maps the wire Action to the stored 0 approve, 1 review, 2 block.
func action(a unfraudv1.Action) int16 {
	if a == unfraudv1.Action_ACTION_UNSPECIFIED {
		return 0
	}
	return int16(a) - 1
}

func loadModels(dir string) (map[string]*model.Model, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]*model.Model{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := dir + "/" + e.Name()
		v, err := scoring.ModelVersion(path)
		if err != nil {
			return nil, err
		}
		if out[v], err = model.LoadArtifact(path); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no models under %s", dir)
	}
	return out, nil
}

func serve(log *slog.Logger, addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Error("http server stopped", "err", err)
	}
}
