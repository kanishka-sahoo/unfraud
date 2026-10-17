// Package api serves the web app: Cases, Decisions, graph neighbourhoods,
// benchmarks, live Decisions over server-sent events, and replay control.
// The routes are documented in api/openapi.yaml.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/cases"
	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
	"github.com/kanishka-sahoo/unfraud/internal/store"
	"github.com/kanishka-sahoo/unfraud/results"
)

// Config wires the API to the rest of the cluster.
type Config struct {
	Store *store.Store
	Log   *slog.Logger
	// GraphURL returns graph-svc's base URL for a Profile.
	GraphURL func(profile string) string
	// Stream opens a live feed of Decisions for a Profile.
	Stream func(ctx context.Context, profile string, send func(LiveDecision) bool) error
	// Replays creates and lists replay Jobs; nil disables replay control.
	Replays Replays
	// AssistantURL is the assistant service's base URL, which answers
	// Investigations; empty turns the assistant off.
	AssistantURL string
}

// Replays is implemented against the Kubernetes API (kube.go).
type Replays interface {
	List(ctx context.Context) ([]Replay, error)
	Start(ctx context.Context, req ReplayRequest) (Replay, error)
}

type Replay struct {
	Name      string     `json:"name"`
	Profile   string     `json:"profile"`
	Speed     float64    `json:"speed"`
	Status    string     `json:"status"` // running, succeeded, failed
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
}

type ReplayRequest struct {
	Profile string  `json:"profile"`
	Speed   float64 `json:"speed"`
	Limit   int     `json:"limit"`
}

// LiveDecision is one Decision on the live stream.
type LiveDecision struct {
	Stage       int16     `json:"stage"`
	EventID     string    `json:"event_id"`
	EventTime   time.Time `json:"event_time"`
	Action      int16     `json:"action"`
	Probability float64   `json:"probability"`
	ReasonCodes []string  `json:"reason_codes"`
	LatencyMs   int64     `json:"latency_ms"`
}

// Handler returns the API's routes.
func Handler(cfg Config) http.Handler {
	h := &handler{cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/profiles", h.profiles)
	mux.HandleFunc("GET /api/overview/{profile}", h.overview)
	mux.HandleFunc("GET /api/cases", h.listCases)
	mux.HandleFunc("GET /api/cases/{profile}/{event_id}", h.getCase)
	mux.HandleFunc("POST /api/cases/{profile}/{event_id}/disposition", h.dispose)
	mux.HandleFunc("POST /api/cases/{profile}/{event_id}/summary", h.requestSummary)
	mux.HandleFunc("GET /api/graph/{profile}/{node}", h.graph)
	mux.HandleFunc("GET /api/models", h.models)
	mux.HandleFunc("GET /api/benchmarks", h.benchmarks)
	mux.HandleFunc("GET /api/stream/{profile}", h.stream)
	mux.HandleFunc("POST /api/investigations", h.assistant)
	mux.HandleFunc("GET /api/investigations", h.assistant)
	mux.HandleFunc("GET /api/investigations/{id}", h.assistant)
	mux.HandleFunc("POST /api/investigations/{id}/ask", h.assistant)
	mux.HandleFunc("GET /api/replays", h.listReplays)
	mux.HandleFunc("POST /api/replays", h.startReplay)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}

type handler struct{ cfg Config }

func (h *handler) reply(w http.ResponseWriter, v any, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case err != nil:
		h.cfg.Log.Error("request failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
}

func (h *handler) profiles(w http.ResponseWriter, r *http.Request) {
	ps, err := h.cfg.Store.Profiles(r.Context())
	h.reply(w, ps, err)
}

func (h *handler) overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.cfg.Store.Overview(r.Context(), r.PathValue("profile"))
	h.reply(w, struct {
		store.Overview
		Recent []caseItem `json:"recent_cases"`
	}{o, withPayment(o.Recent)}, err)
}

// caseItem is a queue row with the payment it is about.
type caseItem struct {
	store.CaseListItem
	Payment []cases.Fact `json:"payment"`
}

func withPayment(cs []store.CaseListItem) []caseItem {
	out := make([]caseItem, len(cs))
	for i, c := range cs {
		out[i] = caseItem{CaseListItem: c, Payment: []cases.Fact{}}
		p, err := registry.Get(c.Profile)
		if err != nil {
			continue
		}
		for _, f := range cases.Facts(p, &store.CaseContributions{Values: c.Values}) {
			if slices.Contains(p.Headline, f.Feature) {
				out[i].Payment = append(out[i].Payment, f)
			}
		}
	}
	return out
}

func (h *handler) listCases(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	minAction, _ := strconv.Atoi(q.Get("min_action"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if q.Get("profile") == "" {
		http.Error(w, "profile is required", http.StatusBadRequest)
		return
	}
	cs, err := h.cfg.Store.ListCases(r.Context(), store.CaseFilter{
		Profile: q.Get("profile"), Status: q.Get("status"), MinAction: int16(minAction),
		Sort: q.Get("sort"), Limit: limit, Offset: offset, Entity: q.Get("entity"),
	})
	if err != nil && strings.HasPrefix(err.Error(), "unknown") {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.reply(w, withPayment(cs), err)
}

type caseDetail struct {
	Case      store.Case       `json:"case"`
	Decisions []store.Decision `json:"decisions"`
	// Facts describe the Event's feature values, the payment's first.
	Facts []cases.Fact `json:"facts"`
}

func (h *handler) getCase(w http.ResponseWriter, r *http.Request) {
	k := store.CaseKey{Profile: r.PathValue("profile"), EventID: r.PathValue("event_id")}
	c, err := h.cfg.Store.GetCase(r.Context(), k)
	if err != nil {
		h.reply(w, nil, err)
		return
	}
	ds, err := h.cfg.Store.EventDecisions(r.Context(), k)
	d := caseDetail{Case: c, Decisions: ds, Facts: []cases.Fact{}}
	if p, perr := registry.Get(c.Profile); perr == nil {
		if fs := cases.Facts(p, c.Contributions); fs != nil {
			d.Facts = fs
		}
	}
	h.reply(w, d, err)
}

func (h *handler) requestSummary(w http.ResponseWriter, r *http.Request) {
	k := store.CaseKey{Profile: r.PathValue("profile"), EventID: r.PathValue("event_id")}
	err := h.cfg.Store.RequestSummary(r.Context(), k)
	h.reply(w, map[string]string{"summary_status": "pending"}, err)
}

func (h *handler) dispose(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Disposition string `json:"disposition"`
		Note        string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	switch body.Disposition {
	case "confirmed", "dismissed", "escalated":
	default:
		http.Error(w, "disposition must be confirmed, dismissed or escalated", http.StatusBadRequest)
		return
	}
	k := store.CaseKey{Profile: r.PathValue("profile"), EventID: r.PathValue("event_id")}
	err := h.cfg.Store.SetDisposition(r.Context(), k, body.Disposition, body.Note)
	h.reply(w, map[string]string{"disposition": body.Disposition}, err)
}

func (h *handler) graph(w http.ResponseWriter, r *http.Request) {
	base := h.cfg.GraphURL(r.PathValue("profile"))
	hops := r.URL.Query().Get("hops")
	if hops == "" {
		hops = "2"
	}
	u := base + "/neighborhood?node=" + url.QueryEscape(r.PathValue("node")) + "&hops=" + url.QueryEscape(hops)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "graph-svc unavailable for this profile", http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
	w.WriteHeader(res.StatusCode)
	buf := make([]byte, 32<<10)
	for {
		n, err := res.Body.Read(buf)
		w.Write(buf[:n])
		if err != nil {
			return
		}
	}
}

// assistant forwards Investigations to the assistant service, streaming
// answers through as they are written.
func (h *handler) assistant(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AssistantURL == "" {
		http.Error(w, "the assistant is off", http.StatusNotImplemented)
		return
	}
	u := h.cfg.AssistantURL + strings.TrimPrefix(r.URL.Path, "/api")
	if r.URL.RawQuery != "" {
		u += "?" + r.URL.RawQuery
	}
	req, _ := http.NewRequestWithContext(r.Context(), r.Method, u, http.MaxBytesReader(w, r.Body, 1<<15))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		if r.Context().Err() == nil {
			http.Error(w, "the assistant is unavailable", http.StatusBadGateway)
		}
		return
	}
	defer res.Body.Close()
	for _, k := range []string{"Content-Type", "Cache-Control", "X-Accel-Buffering"} {
		if v := res.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 16<<10)
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func (h *handler) models(w http.ResponseWriter, r *http.Request) {
	ms, err := h.cfg.Store.ModelUses(r.Context())
	h.reply(w, ms, err)
}

type benchmark struct {
	Name     string          `json:"name"`
	Markdown string          `json:"markdown"`
	Data     json.RawMessage `json:"data,omitempty"`
}

func (h *handler) benchmarks(w http.ResponseWriter, _ *http.Request) {
	names, err := fs.Glob(results.FS, "*.md")
	if err != nil {
		h.reply(w, nil, err)
		return
	}
	out := []benchmark{}
	for _, n := range names {
		md, _ := results.FS.ReadFile(n)
		data, _ := results.FS.ReadFile(strings.TrimSuffix(n, ".md") + ".json")
		out = append(out, benchmark{Name: strings.TrimSuffix(n, ".md"), Markdown: string(md), Data: data})
	}
	h.reply(w, out, nil)
}

// stream sends live Decisions as server-sent events, at most 20 a second
// per client (the rest are counted, not sent).
func (h *handler) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok || h.cfg.Stream == nil {
		http.Error(w, "streaming unsupported", http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	// A comment right away (and every 15s) lets proxies that wait for the
	// first body bytes pass the stream on, and keeps idle connections open.
	var mu sync.Mutex
	write := func(b []byte) error {
		mu.Lock()
		defer mu.Unlock()
		if _, err := w.Write(b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	write([]byte(": connected\n\n"))
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-t.C:
				if write([]byte(": keep-alive\n\n")) != nil {
					return
				}
			}
		}
	}()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	dropped := 0
	err := h.cfg.Stream(r.Context(), r.PathValue("profile"), func(d LiveDecision) bool {
		select {
		case <-ticker.C:
		default:
			dropped++
			return r.Context().Err() == nil
		}
		b, _ := json.Marshal(struct {
			LiveDecision
			Skipped int `json:"skipped"`
		}{d, dropped})
		dropped = 0
		return write(append(append([]byte("data: "), b...), '\n', '\n')) == nil
	})
	if err != nil && r.Context().Err() == nil {
		h.cfg.Log.Warn("stream ended", "err", err)
	}
}

func (h *handler) listReplays(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Replays == nil {
		h.reply(w, []Replay{}, nil)
		return
	}
	rs, err := h.cfg.Replays.List(r.Context())
	h.reply(w, rs, err)
}

func (h *handler) startReplay(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Replays == nil {
		http.Error(w, "replay control is off", http.StatusNotImplemented)
		return
	}
	var req ReplayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Speed < 0 || req.Limit < 0 || req.Profile == "" {
		http.Error(w, "profile is required; speed and limit must not be negative", http.StatusBadRequest)
		return
	}
	rs, err := h.cfg.Replays.List(r.Context())
	if err != nil {
		h.reply(w, nil, err)
		return
	}
	for _, x := range rs {
		if x.Status == "running" {
			http.Error(w, "a replay is already running: "+x.Name, http.StatusConflict)
			return
		}
	}
	rep, err := h.cfg.Replays.Start(r.Context(), req)
	if err != nil && strings.Contains(err.Error(), "unknown profile") {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.reply(w, rep, err)
}
