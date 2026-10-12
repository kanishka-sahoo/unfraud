// Command casesapi serves the web app's API (internal/api).
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/api"
	"github.com/kanishka-sahoo/unfraud/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("casesapi failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	var (
		brokers   = flag.String("brokers", "localhost:9092", "comma-separated Kafka bootstrap servers")
		listen    = flag.String("listen", ":8080", "HTTP address")
		graphURL  = flag.String("graph-url", "http://graphsvc-%s:8080", "graph-svc URL; %s is the profile")
		namespace = flag.String("namespace", "unfraud", "namespace for replay Jobs")
		replayImg = flag.String("replayer-image", "", "replayer image; empty disables replay control")
		dataPath  = flag.String("data-path", "/data", "dataset folder on the node, for replay Jobs")
	)
	flag.Parse()
	pgURL := os.Getenv("PG_URL")
	if pgURL == "" {
		return errors.New("$PG_URL is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, pgURL)
	if err != nil {
		return err
	}
	defer st.Close()

	seeds := strings.Split(*brokers, ",")
	cfg := api.Config{
		Store:    st,
		Log:      log,
		GraphURL: func(p string) string { return strings.Replace(*graphURL, "%s", p, 1) },
		Stream: func(ctx context.Context, p string, send func(api.LiveDecision) bool) error {
			return stream(ctx, seeds, p, send)
		},
	}
	if *replayImg != "" {
		kr, err := api.InCluster(*namespace, *replayImg, seeds[0], *dataPath)
		if err != nil {
			log.Warn("replay control off: not running in a cluster", "err", err)
		} else {
			cfg.Replays = kr
		}
	}
	srv := &http.Server{Addr: *listen, Handler: api.Handler(cfg), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Info("casesapi listening", "addr", *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// stream tails both Decision topics from their ends, without a consumer
// group, until send returns false or ctx ends.
func stream(ctx context.Context, seeds []string, profile string, send func(api.LiveDecision) bool) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(seeds...),
		kgo.ConsumeTopics("scored."+profile, "revised."+profile),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()))
	if err != nil {
		return err
	}
	defer cl.Close()
	for {
		fs := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err := fs.Err0(); err != nil {
			return err
		}
		keep := true
		fs.EachRecord(func(r *kgo.Record) {
			if !keep {
				return
			}
			var s unfraudv1.Scored
			if proto.Unmarshal(r.Value, &s) != nil {
				return
			}
			a := int16(0)
			if s.Action > unfraudv1.Action_ACTION_UNSPECIFIED {
				a = int16(s.Action) - 1
			}
			keep = send(api.LiveDecision{Stage: int16(s.Stage), EventID: s.EventId, EventTime: time.UnixMilli(s.EventTimeMs),
				Action: a, Probability: s.Probability, ReasonCodes: s.ReasonCodes, LatencyMs: s.ScoredAtMs - s.IngestedAtMs})
		})
		if !keep {
			return nil
		}
	}
}
