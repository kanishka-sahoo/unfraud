// Command assistant is the console's LLM service (ADR-0022). It writes
// Summaries for Cases an analyst asked about: it claims them from a Postgres
// queue, builds each Case Packet, asks a driver (stub, Claude or OpenRouter)
// for a Summary, and keeps it only if it passes the Grounding check,
// retrying once (ADR-0009, ADR-0021). It also serves Investigations, the
// console's assistant chat, over HTTP. The LLM never affects a Decision.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/kanishka-sahoo/unfraud/internal/agent"
	"github.com/kanishka-sahoo/unfraud/internal/cases"
	"github.com/kanishka-sahoo/unfraud/internal/investigate"
	"github.com/kanishka-sahoo/unfraud/internal/llm"
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
		log.Error("assistant failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	var (
		driverName  = flag.String("driver", "stub", "stub, anthropic (any Anthropic-compatible API), openai (any OpenAI-compatible API) or openrouter")
		modelName   = flag.String("model", "claude-haiku-5-5", "model id as the endpoint names it, such as anthropic/claude-haiku-5.5 on OpenRouter")
		baseURL     = flag.String("base-url", "", "API root for anthropic or openai; empty is the Claude API, required for openai")
		effort      = flag.String("effort", "low", "anthropic: output_config.effort; empty leaves the model's default")
		temperature = flag.String("temperature", "", "openai and openrouter: sampling temperature; empty sends none")
		providers   = flag.String("providers", "", "openrouter: comma-separated providers to pin, fallbacks off")
		perMinute   = flag.Int("per-minute", 0, "rate limit on Summary driver calls; 0 = none")
		limit       = flag.Int("limit", 0, "stop after this many Summaries; 0 = run forever")
		listen      = flag.String("listen", ":8080", "address for Investigations, /metrics and /healthz")
		graphURL    = flag.String("graph-url", "http://graphsvc-%s:8080", "graph-svc URL; %s is the profile")
		agentModel  = flag.String("agent-model", "claude-haiku-5-5", "Investigations: Claude model; needs $ANTHROPIC_API_KEY, else a scripted stub answers")
		agentURL    = flag.String("agent-base-url", "", "Investigations: Anthropic-compatible API root; empty is the Claude API")
		agentEffort = flag.String("agent-effort", "medium", "Investigations: output_config.effort; empty leaves the model's default")
		agentBudget = flag.Int64("agent-budget", 400_000, "Investigations: most tokens one conversation may use; 0 = no limit")
		agentConc   = flag.Int("agent-concurrency", 4, "Investigations: questions answered at once")
	)
	flag.Parse()
	pgURL := os.Getenv("PG_URL")
	if pgURL == "" {
		return errors.New("$PG_URL is required")
	}
	driver, label, err := newDriver(log, *driverName, *modelName, *baseURL, *effort, *temperature, *providers)
	if err != nil {
		return err
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
	lookup := investigate.Lookup{Store: st, Graph: investigate.HTTPGraph{URL: func(p string) string { return strings.Replace(*graphURL, "%s", p, 1) }}}
	ag := &agent.Agent{Store: st, Lookup: lookup, Model: newAgentModel(log, *agentModel, *agentURL, *agentEffort), Budget: *agentBudget,
		OnTurn: observeTurn}
	go serve(log, *listen, newServer(log, ag, *agentConc))
	log.Info("assistant starting", "driver", *driverName, "model", label, "agent_model", ag.Model.Name())

	var tick <-chan time.Time
	if *perMinute > 0 {
		t := time.NewTicker(time.Minute / time.Duration(*perMinute))
		defer t.Stop()
		tick = t.C
	}
	done := 0
	for *limit == 0 || done < *limit {
		batch, err := st.ClaimSummaries(ctx, 4)
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
			case <-time.After(time.Second):
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
			if err := summarize(ctx, log, st, lookup, driver, label, c); err != nil {
				return err
			}
			done++
		}
	}
	return nil
}

// newAgentModel picks the Investigations model: Claude with a key (or an
// Anthropic-compatible endpoint), else the scripted stub.
func newAgentModel(log *slog.Logger, model, baseURL, effort string) agent.Model {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" && baseURL == "" {
		log.Warn("$ANTHROPIC_API_KEY is not set; the assistant chat answers from a script (set it in .env, then make secrets)")
		return agent.Stub{}
	}
	return agent.Claude{Key: key, Model: model, BaseURL: baseURL, Effort: effort}
}

// newDriver picks the Summary writer. Each API driver reads its key from
// the environment (set from .env by make secrets); a missing key falls back
// to the template writer, so a cluster without one still runs, and the
// console labels its Summaries as templates.
func newDriver(log *slog.Logger, name, model, baseURL, effort, temperature, providers string) (llm.Driver, string, error) {
	keyVar := map[string]string{"anthropic": "ANTHROPIC_API_KEY", "openai": "OPENAI_API_KEY", "openrouter": "OPENROUTER_API_KEY"}[name]
	key := os.Getenv(keyVar)
	var temp *float64
	if temperature != "" {
		t, err := strconv.ParseFloat(temperature, 64)
		if err != nil {
			return nil, "", fmt.Errorf("-temperature: %w", err)
		}
		temp = &t
	}
	switch name {
	case "stub":
		return llm.Stub{}, "stub", nil
	case "anthropic":
		if key == "" && baseURL == "" {
			break
		}
		return llm.Anthropic{Key: key, Model: model, BaseURL: baseURL, Effort: effort}, model, nil
	case "openai":
		if baseURL == "" {
			return nil, "", errors.New("the openai driver needs -base-url, such as https://api.openai.com/v1")
		}
		return llm.OpenAI{Key: key, Model: model, BaseURL: baseURL, Temperature: temp}, model, nil
	case "openrouter":
		if key == "" {
			break
		}
		var pin []string
		if providers != "" {
			pin = strings.Split(providers, ",")
		}
		return llm.OpenAI{Key: key, Model: model, BaseURL: cmp.Or(baseURL, llm.OpenRouterURL), OpenRouter: true,
			Providers: pin, Temperature: temp}, model, nil
	default:
		return nil, "", fmt.Errorf("unknown driver %q", name)
	}
	log.Warn("$"+keyVar+" is not set; writing template summaries (set it in .env, then make secrets)", "driver", name)
	return llm.Stub{}, "stub", nil
}

// summarize makes up to two attempts at a grounded Summary for one Case.
func summarize(ctx context.Context, log *slog.Logger, st *store.Store, lookup investigate.Lookup, d llm.Driver, label string, c store.Case) error {
	key := store.CaseKey{Profile: c.Profile, EventID: c.EventID}
	pk, err := lookup.Packet(ctx, c)
	if err != nil {
		if ctx.Err() != nil {
			return st.RequeueSummary(context.Background(), key)
		}
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

func serve(log *slog.Logger, addr string, h http.Handler) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("/investigations", h)
	mux.Handle("/investigations/", h)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Error("http server stopped", "err", err)
	}
}
