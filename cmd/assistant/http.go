package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/kanishka-sahoo/unfraud/internal/agent"
	"github.com/kanishka-sahoo/unfraud/internal/store"
)

var (
	questions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_assistant_questions_total", Help: "Investigation questions by status: answered, stopped, error, refused.",
	}, []string{"status"})
	lookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "unfraud_assistant_lookups_total", Help: "Investigation lookups by tool and status: done, error.",
	}, []string{"tool", "status"})
	unverifiedAnswers = promauto.NewCounter(prometheus.CounterOpts{
		Name: "unfraud_assistant_unverified_answers_total", Help: "Answers with a number or ID nothing looked up contains.",
	})
	agentTokens = promauto.NewCounter(prometheus.CounterOpts{
		Name: "unfraud_assistant_tokens_total", Help: "Tokens Investigations used, input and output.",
	})
	agentCost = promauto.NewCounter(prometheus.CounterOpts{
		Name: "unfraud_assistant_cost_usd_total", Help: "Investigation cost at list price.",
	})
)

func observeTurn(t agent.Turn) {
	questions.WithLabelValues(t.Status).Inc()
	for _, s := range t.Steps {
		if s.Tool != "" {
			lookups.WithLabelValues(s.Tool, s.Status).Inc()
		}
	}
	if len(t.Unverified) > 0 {
		unverifiedAnswers.Inc()
	}
	agentTokens.Add(float64(t.Tokens))
	agentCost.Add(t.CostUSD)
}

// server serves Investigations to cases-api, which proxies the console's
// /api/investigations to it.
type server struct {
	log   *slog.Logger
	agent *agent.Agent
	slots chan struct{}
	mux   *http.ServeMux
}

func newServer(log *slog.Logger, a *agent.Agent, concurrency int) http.Handler {
	s := &server{log: log, agent: a, slots: make(chan struct{}, max(concurrency, 1)), mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /investigations", s.create)
	s.mux.HandleFunc("GET /investigations", s.list)
	s.mux.HandleFunc("GET /investigations/{id}", s.get)
	s.mux.HandleFunc("POST /investigations/{id}/ask", s.ask)
	return s.mux
}

func (s *server) reply(w http.ResponseWriter, v any, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case err != nil && strings.HasPrefix(err.Error(), "invalid input"):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case err != nil:
		s.log.Error("request failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
}

// view is an Investigation as the console shows it.
type view struct {
	store.Investigation
	Turns       []agent.Turn `json:"turns"`
	Suggestions []string     `json:"suggestions"`
	TokensUsed  int64        `json:"tokens_used"`
	TokenBudget int64        `json:"token_budget"`
	Model       string       `json:"model"`
}

func (s *server) view(r *http.Request, inv store.Investigation) (view, error) {
	ms, err := s.agent.Store.Messages(r.Context(), inv.ID, false)
	if err != nil {
		return view{}, err
	}
	used, err := s.agent.Store.InvestigationTokens(r.Context(), inv.ID)
	return view{Investigation: inv, Turns: agent.Render(inv, ms), Suggestions: agent.Suggestions(inv), TokensUsed: used,
		TokenBudget: s.agent.Budget, Model: s.agent.Model.Name()}, err
}

func (s *server) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profile string `json:"profile"`
		CaseID  string `json:"case_id"`
		Card    string `json:"card"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil || body.Profile == "" {
		http.Error(w, "need JSON with a profile, and optionally a case_id or card", http.StatusBadRequest)
		return
	}
	inv, err := s.agent.Create(r.Context(), body.Profile, body.CaseID, body.Card)
	if err != nil {
		s.reply(w, nil, err)
		return
	}
	v, err := s.view(r, inv)
	s.reply(w, v, err)
}

func (s *server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("profile") == "" {
		http.Error(w, "profile is required", http.StatusBadRequest)
		return
	}
	is, err := s.agent.Store.ListInvestigations(r.Context(), q.Get("profile"), q.Get("case_id"), 20)
	s.reply(w, is, err)
}

func (s *server) get(w http.ResponseWriter, r *http.Request) {
	inv, err := s.agent.Store.GetInvestigation(r.Context(), r.PathValue("id"))
	if err != nil {
		s.reply(w, nil, err)
		return
	}
	v, err := s.view(r, inv)
	s.reply(w, v, err)
}

// ask answers a question as server-sent events: the question's sequence
// number, each lookup as it starts and ends, the answer's text as it is
// written, and the finished turn.
func (s *server) ask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Question string `json:"question"`
		FromSeq  int    `json:"from_seq"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if len(body.Question) > 4000 {
		http.Error(w, "the question is too long", http.StatusBadRequest)
		return
	}
	inv, err := s.agent.Store.GetInvestigation(r.Context(), r.PathValue("id"))
	if err != nil {
		s.reply(w, nil, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	var mu sync.Mutex
	send := func(e agent.Event) {
		b, _ := json.Marshal(e)
		mu.Lock()
		defer mu.Unlock()
		w.Write(append(append([]byte("data: "), b...), '\n', '\n'))
		flusher.Flush()
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				mu.Lock()
				w.Write([]byte(": keep-alive\n\n"))
				flusher.Flush()
				mu.Unlock()
			}
		}
	}()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		send(agent.Event{Type: "error", Message: "The assistant is busy with other questions. Please try again in a moment."})
		return
	}
	err = s.agent.Ask(r.Context(), inv, body.Question, body.FromSeq, send)
	switch {
	case err == nil:
	case errors.Is(err, agent.ErrBusy), errors.Is(err, agent.ErrBudget):
		msg := err.Error()
		send(agent.Event{Type: "error", Message: strings.ToUpper(msg[:1]) + msg[1:] + "."})
	case strings.HasPrefix(err.Error(), "invalid input"):
		send(agent.Event{Type: "error", Message: "Please type a question first."})
	case r.Context().Err() == nil:
		s.log.Error("investigation failed", "investigation", inv.ID, "err", err)
		send(agent.Event{Type: "error", Message: "Sorry, something went wrong. Please try again."})
	}
}
