// Command summarizer writes LLM Summaries for Cases. It claims Cases from a
// Postgres queue (strictest and highest-scoring first), builds each Case
// Packet, asks a driver (stub or OpenRouter) for a Summary, and keeps it only
// if it passes the Grounding check, retrying once (ADR-0009). The LLM never
// affects a Decision.
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

	"github.com/kanishka-sahoo/unfraud/internal/cases"
	"github.com/kanishka-sahoo/unfraud/internal/llm"
	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
	"github.com/kanishka-sahoo/unfraud/internal/store"
)

var (
	summaries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_summarizer_summaries_total", Help: "Summaries by outcome: done, failed.",
	}, []string{"model", "outcome"})
	attempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_summarizer_attempts_total", Help: "Driver calls by result: grounded, ungrounded, error.",
	}, []string{"model", "result"})
	latency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "unfraud_summarizer_latency_seconds", Help: "Driver latency per call.",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
	}, []string{"model"})
	cost = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_summarizer_cost_usd_total", Help: "Cost reported by the provider.",
	}, []string{"model"})
)

// Record is what the cases table stores in summary.
type Record struct {
	cases.Summary
	Grounded bool         `json:"grounded"`
	Problems []string     `json:"problems,omitempty"`
	Meta     llm.Meta     `json:"meta"`
	Attempts int          `json:"attempts"`
	Packet   cases.Packet `json:"packet"`
	Error    string       `json:"error,omitempty"`
	At       time.Time    `json:"at"`
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("summarizer failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	var (
		driverName = flag.String("driver", "stub", "stub or openrouter")
		modelName  = flag.String("model", "anthropic/claude-haiku-4.5", "OpenRouter model")
		providers  = flag.String("providers", "", "comma-separated OpenRouter providers to pin, fallbacks off")
		minAction  = flag.Int("min-action", 1, "summarize Cases at least this strict: 1 review, 2 block")
		perMinute  = flag.Int("per-minute", 0, "rate limit on driver calls; 0 = none")
		limit      = flag.Int("limit", 0, "stop after this many Summaries; 0 = run forever")
		listen     = flag.String("listen", ":8080", "address for /metrics and /healthz")
	)
	flag.Parse()
	pgURL := os.Getenv("PG_URL")
	if pgURL == "" {
		return errors.New("$PG_URL is required")
	}
	var driver llm.Driver = llm.Stub{}
	label := "stub"
	if *driverName == "openrouter" {
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			return errors.New("$OPENROUTER_API_KEY is required for the openrouter driver")
		}
		var pin []string
		if *providers != "" {
			pin = strings.Split(*providers, ",")
		}
		driver, label = llm.OpenRouter{Key: key, Model: *modelName, Providers: pin}, *modelName
	} else if *driverName != "stub" {
		return fmt.Errorf("unknown driver %q", *driverName)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, pgURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if n, err := st.ResetStaleSummaries(ctx, 10*time.Minute); err != nil {
		return err
	} else if n > 0 {
		log.Info("requeued summaries left running", "cases", n)
	}
	go serve(log, *listen)
	log.Info("summarizer starting", "driver", *driverName, "model", label, "min_action", *minAction)

	var tick <-chan time.Time
	if *perMinute > 0 {
		t := time.NewTicker(time.Minute / time.Duration(*perMinute))
		defer t.Stop()
		tick = t.C
	}
	done := 0
	for *limit == 0 || done < *limit {
		batch, err := st.ClaimSummaries(ctx, 4, int16(*minAction))
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if len(batch) == 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
			}
			continue
		}
		for _, c := range batch {
			if tick != nil {
				select {
				case <-ctx.Done():
					return nil
				case <-tick:
				}
			}
			if err := summarize(ctx, log, st, driver, label, c); err != nil {
				return err
			}
			done++
		}
	}
	return nil
}

// summarize makes up to two attempts at a grounded Summary for one Case.
func summarize(ctx context.Context, log *slog.Logger, st *store.Store, d llm.Driver, label string, c store.Case) error {
	key := store.CaseKey{Profile: c.Profile, EventID: c.EventID}
	p, err := registry.Get(c.Profile)
	if err != nil {
		return err
	}
	pk, err := cases.Build(p, c)
	if err != nil {
		return st.SaveSummary(ctx, key, "skipped", Record{Error: err.Error(), At: time.Now()})
	}
	rec := Record{Packet: pk}
	for attempt := 1; attempt <= 2; attempt++ {
		rec.Attempts = attempt
		s, meta, err := d.Summarize(ctx, pk)
		rec.Meta = meta
		latency.WithLabelValues(label).Observe(float64(meta.LatencyMs) / 1000)
		cost.WithLabelValues(label).Add(meta.CostUSD)
		if err != nil {
			if ctx.Err() != nil {
				return st.RequeueSummary(context.Background(), key)
			}
			attempts.WithLabelValues(label, "error").Inc()
			rec.Error = err.Error()
			log.Warn("summary attempt failed", "case", pk.CaseID, "attempt", attempt, "err", err)
			continue
		}
		rec.Summary, rec.Error = s, ""
		rec.Problems = cases.Ground(s.All(), pk)
		rec.Grounded = len(rec.Problems) == 0
		if rec.Grounded {
			attempts.WithLabelValues(label, "grounded").Inc()
			break
		}
		attempts.WithLabelValues(label, "ungrounded").Inc()
		log.Warn("summary not grounded", "case", pk.CaseID, "attempt", attempt, "problems", rec.Problems)
	}
	rec.At = time.Now().UTC()
	status := "done"
	if !rec.Grounded {
		status = "failed" // never shown to analysts
	}
	summaries.WithLabelValues(label, status).Inc()
	return st.SaveSummary(ctx, key, status, rec)
}

func serve(log *slog.Logger, addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Error("http server stopped", "err", err)
	}
}
