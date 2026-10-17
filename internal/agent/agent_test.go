package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/jackc/pgx/v5"

	"github.com/kanishka-sahoo/unfraud/internal/graph"
	"github.com/kanishka-sahoo/unfraud/internal/investigate"
	"github.com/kanishka-sahoo/unfraud/internal/store"
)

// testStore needs a disposable Postgres:
// UNFRAUD_PG_URL=postgres://postgres:test@localhost:5433/postgres
func testStore(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("UNFRAUD_PG_URL")
	if url == "" {
		t.Skip("UNFRAUD_PG_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	db := fmt.Sprintf("test_agent_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+db); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgx.ParseConfig(url)
	s, err := store.Open(ctx, fmt.Sprintf("postgres://%s:%s@%s:%d/%s", cfg.User, cfg.Password, cfg.Host, cfg.Port, db))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		admin.Exec(ctx, "DROP DATABASE "+db)
		admin.Close(ctx)
	})
	return s
}

var at = time.Date(2019, 3, 4, 15, 30, 0, 0, time.UTC)

// seed stores alerts: the one investigated (a1 on card 77-1), an earlier one
// on the card, and two other cards' alerts at the same merchant.
func seed(t *testing.T, s *store.Store) {
	ctx := context.Background()
	add := func(id, card string, dt time.Duration, action int16, amount float64) {
		d := store.Decision{Profile: "tabformer", EventID: id, Stage: 1, EventTime: at.Add(dt), Probability: 0.9,
			ModelAction: action, Action: action, ModelVersion: "m1", ScoredAt: at, IngestedAt: at,
			Entities: map[string]string{"payer": card, "payee": "m9"}}
		if _, _, err := s.RecordDecisions(ctx, []store.Decision{d}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetContributions(ctx, store.CaseKey{Profile: "tabformer", EventID: id}, store.CaseContributions{
			Top:    []store.Contribution{{Feature: "amount_usd", Value: amount, SHAP: 1.2}},
			Values: map[string]any{"amount_usd": amount, "mcc": "5411", "channel": "online"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	add("a1", "77-1", 0, 2, 59.85)
	add("a0", "77-1", -48*time.Hour, 1, 500)
	add("b1", "88-2", -2*time.Hour, 2, 12.5)
	add("c1", "99-3", 3*time.Hour, 1, 7.25)
	if err := s.SetDisposition(ctx, store.CaseKey{Profile: "tabformer", EventID: "b1"}, "confirmed", ""); err != nil {
		t.Fatal(err)
	}
}

type fakeGraph struct{}

func (fakeGraph) Neighborhood(_ context.Context, _, node string, _ int) (graph.Subgraph, error) {
	return graph.Subgraph{Center: node, Nodes: []graph.GNode{{ID: node, Kind: "payer"}, {ID: "m9", Kind: "payee"},
		{ID: "88-2", Kind: "payer", Fraud: true}, {ID: "99-3", Kind: "payer"}}}, nil
}

func newAgent(t *testing.T, m Model) (*Agent, store.Investigation) {
	s := testStore(t)
	seed(t, s)
	a := &Agent{Store: s, Lookup: investigate.Lookup{Store: s, Graph: fakeGraph{}}, Model: m, Budget: 1_000_000}
	inv, err := a.Create(context.Background(), "tabformer", "a1", "")
	if err != nil {
		t.Fatal(err)
	}
	return a, inv
}

func ask(t *testing.T, a *Agent, inv store.Investigation, q string, from int) []Event {
	t.Helper()
	var mu sync.Mutex
	var evs []Event
	if err := a.Ask(context.Background(), inv, q, from, func(e Event) { mu.Lock(); evs = append(evs, e); mu.Unlock() }); err != nil {
		t.Fatal(err)
	}
	return evs
}

func turns(t *testing.T, a *Agent, inv store.Investigation) []Turn {
	ms, err := a.Store.Messages(context.Background(), inv.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return Render(inv, ms)
}

func TestStubLooksUpTheAlertAndAnswersFromIt(t *testing.T) {
	a, inv := newAgent(t, Stub{})
	evs := ask(t, a, inv, "Has this card been flagged before?", 0)
	last := evs[len(evs)-1]
	if last.Type != "done" || last.Turn.Status != "answered" {
		t.Fatalf("last event %+v", last)
	}
	tr := last.Turn
	if len(tr.Steps) != 2 || tr.Steps[0].Tool != "get_alert" || tr.Steps[1].Tool != "card_history" {
		t.Fatalf("steps %+v", tr.Steps)
	}
	for _, s := range tr.Steps {
		if s.Status != "done" || s.Summary == "" || s.Table == nil {
			t.Errorf("step %+v should be done with a summary and a table", s)
		}
	}
	if !strings.Contains(tr.Steps[1].Summary, "2 alerts in 30 days, 2 blocked") && !strings.Contains(tr.Steps[1].Summary, "2 alerts in 30 days") {
		t.Errorf("card history summary %q", tr.Steps[1].Summary)
	}
	if len(tr.Unverified) != 0 {
		t.Errorf("the stub's answer is grounded by construction; unverified %v in %q", tr.Unverified, tr.Answer)
	}
	if tr.Links["77-1"] != "/network/77-1" || tr.Links["a0"] != "/alerts/a0" {
		t.Errorf("links %v", tr.Links)
	}
	var running int
	for _, e := range evs {
		if e.Type == "step" && e.Step.Status == "running" {
			running++
		}
	}
	if running != 2 {
		t.Errorf("%d running step events, want one per lookup", running)
	}
	if inv, _ = a.Store.GetInvestigation(context.Background(), inv.ID); inv.Title != "Has this card been flagged before?" {
		t.Errorf("title %q", inv.Title)
	}
}

// script is a model that replies from a list and records what it was sent.
type script struct {
	mu      sync.Mutex
	replies []func(ctx context.Context) (Reply, error)
	sent    [][]Wire
}

func (s *script) Name() string { return "script" }

func (s *script) Reply(ctx context.Context, req Request, onText func(string)) (Reply, error) {
	s.mu.Lock()
	s.sent = append(s.sent, req.Messages)
	f := s.replies[0]
	if len(s.replies) > 1 {
		s.replies = s.replies[1:]
	}
	s.mu.Unlock()
	r, err := f(ctx)
	if err == nil && r.StopReason == "" {
		r.StopReason = "end_turn"
	}
	return r, err
}

func text(s string) func(context.Context) (Reply, error) {
	return func(context.Context) (Reply, error) {
		b, _ := json.Marshal([]map[string]any{{"type": "thinking", "thinking": "", "signature": "sig-" + s}, {"type": "text", "text": s}})
		return Reply{Content: b, Usage: Usage{InputTokens: 100, OutputTokens: 10}}, nil
	}
}

func lookup(name, input string) func(context.Context) (Reply, error) {
	return func(context.Context) (Reply, error) {
		b := fmt.Sprintf(`[{"type":"thinking","thinking":"","signature":"s"},{"type":"tool_use","id":"t%d","name":%q,"input":%s}]`,
			time.Now().UnixNano(), name, input)
		return Reply{Content: json.RawMessage(b), StopReason: "tool_use"}, nil
	}
}

func TestRewindKeepsTheEarlierConversationByteForByte(t *testing.T) {
	m := &script{replies: []func(context.Context) (Reply, error){
		lookup("card_history", `{"card":"77-1"}`), text("The card had 2 alerts."),
		text("Second answer."), text("Edited answer."),
	}}
	a, inv := newAgent(t, m)
	ask(t, a, inv, "First?", 0)
	ask(t, a, inv, "Second?", 0)
	tr := turns(t, a, inv)
	if len(tr) != 2 {
		t.Fatalf("%d turns", len(tr))
	}
	ask(t, a, inv, "Second, edited?", tr[1].Seq)
	tr = turns(t, a, inv)
	if len(tr) != 2 || tr[1].Question != "Second, edited?" || tr[1].Answer != "Edited answer." {
		t.Fatalf("after rewind: %+v", tr)
	}
	// The request after the rewind starts with exactly what the first
	// turn sent and received, thinking blocks included.
	before, edited := m.sent[2], m.sent[3]
	if len(edited) != 5 { // question, lookup, results, answer, edited question
		t.Fatalf("%d messages after the rewind", len(edited))
	}
	for i := range 4 {
		if !bytes.Equal(before[i].Content, edited[i].Content) {
			t.Errorf("message %d changed:\n%s\n%s", i, before[i].Content, edited[i].Content)
		}
	}
	if !strings.Contains(string(edited[0].Content), "time_being_investigated") || !strings.Contains(string(edited[3].Content), `"signature":"sig-The card had 2 alerts."`) {
		t.Errorf("context or thinking block missing: %s / %s", edited[0].Content, edited[3].Content)
	}
	all, _ := a.Store.Messages(context.Background(), inv.ID, true)
	var superseded int
	for _, m := range all {
		if m.SupersededAt != nil {
			superseded++
		}
	}
	if len(all) != 8 || superseded != 2 {
		t.Errorf("%d stored messages, %d superseded; want 8 kept, 2 set aside", len(all), superseded)
	}
}

func TestStopLeavesAConversationThatCanContinue(t *testing.T) {
	started := make(chan struct{})
	m := &script{replies: []func(context.Context) (Reply, error){
		func(ctx context.Context) (Reply, error) { close(started); <-ctx.Done(); return Reply{}, ctx.Err() },
		text("After the stop."),
	}}
	a, inv := newAgent(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	if err := a.Ask(ctx, inv, "Slow question?", 0, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	ask(t, a, inv, "Next?", 0)
	tr := turns(t, a, inv)
	if len(tr) != 2 || tr[0].Status != "stopped" || tr[1].Answer != "After the stop." {
		t.Fatalf("turns %+v", tr)
	}
	// Roles alternate, so the API accepts the conversation.
	last := m.sent[len(m.sent)-1]
	for i := 1; i < len(last); i++ {
		if last[i].Role == last[i-1].Role {
			t.Fatalf("messages %d and %d are both %s", i-1, i, last[i].Role)
		}
	}
}

func TestNumbersNothingLookedUpAreMarked(t *testing.T) {
	m := &script{replies: []func(context.Context) (Reply, error){
		lookup("card_history", `{"card":"77-1"}`),
		text("Card 77-1 had 2 alerts totalling $559.85 over 30 days, and 4,321 payments last year."),
	}}
	a, inv := newAgent(t, m)
	ask(t, a, inv, "History?", 0)
	tr := turns(t, a, inv)[0]
	if fmt.Sprint(tr.Unverified) != "[4,321]" {
		t.Fatalf("unverified %v in %q", tr.Unverified, tr.Answer)
	}
}

func TestLookupsPerQuestionAreCapped(t *testing.T) {
	var replies []func(context.Context) (Reply, error)
	for range MaxLookups + 2 {
		replies = append(replies, lookup("card_history", `{"card":"77-1"}`))
	}
	m := &script{replies: append(replies, text("Done."))}
	a, inv := newAgent(t, m)
	ask(t, a, inv, "Dig?", 0)
	tr := turns(t, a, inv)[0]
	var skipped int
	for _, s := range tr.Steps {
		if s.Status == "error" {
			skipped++
		}
	}
	if len(tr.Steps) != MaxLookups+2 || skipped != 2 || tr.Answer != "Done." {
		t.Fatalf("%d steps, %d skipped, answer %q", len(tr.Steps), skipped, tr.Answer)
	}
}

func TestBudgetStopsLongConversations(t *testing.T) {
	a, inv := newAgent(t, &script{replies: []func(context.Context) (Reply, error){text("One.")}})
	a.Budget = 50
	ask(t, a, inv, "First?", 0)
	if err := a.Ask(context.Background(), inv, "Second?", 0, func(Event) {}); err != ErrBudget {
		t.Fatalf("second question: %v, want ErrBudget", err)
	}
}

func TestToolsExplainBadInputToTheModel(t *testing.T) {
	m := &script{replies: []func(context.Context) (Reply, error){lookup("get_alert", `{"alert_id":"nope"}`), text("No such alert.")}}
	a, inv := newAgent(t, m)
	ask(t, a, inv, "Open nope", 0)
	tr := turns(t, a, inv)[0]
	if tr.Steps[0].Status != "error" || tr.Steps[0].Error != "There is no alert nope" {
		t.Fatalf("step %+v", tr.Steps[0])
	}
	if !strings.Contains(string(m.sent[1][2].Content), `"is_error":true`) {
		t.Errorf("tool result %s", m.sent[1][2].Content)
	}
}

func TestClaudeRequest(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		if !strings.Contains(r.Header.Get("anthropic-beta"), "server-side-fallback-2026-07-01") {
			t.Errorf("beta header %q", r.Header.Get("anthropic-beta"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []string{
			`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"stop_reason":null,"usage":{"input_tokens":1000,"output_tokens":1,"cache_read_input_tokens":500}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello "}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"there."}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}`,
			`{"type":"message_stop"}`,
		} {
			var typ struct{ Type string }
			json.Unmarshal([]byte(e), &typ)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ.Type, e)
		}
	}))
	defer srv.Close()
	c := Claude{Key: "k", Model: "claude-sonnet-5-5", Effort: "medium", Options: []option.RequestOption{option.WithBaseURL(srv.URL)}}
	prior := json.RawMessage(`[{"type":"thinking","thinking":"","signature":"keep-me"},{"type":"text","text":"Earlier."}]`)
	var streamed strings.Builder
	r, err := c.Reply(context.Background(), Request{System: SystemPrompt, Tools: Specs, Messages: []Wire{
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"Hi"}]`)},
		{Role: "assistant", Content: prior},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"Again"}]`)},
	}}, func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "Hello there." || r.StopReason != "end_turn" || r.Model != "claude-sonnet-5-5" {
		t.Errorf("reply %+v, streamed %q", r, streamed.String())
	}
	if !strings.Contains(string(r.Content), `"signature":"abc"`) || !strings.Contains(string(r.Content), `"text":"Hello there."`) {
		t.Errorf("content %s", r.Content)
	}
	if r.CostUSD <= 0 {
		t.Errorf("cost %v", r.CostUSD)
	}
	msgs := got["messages"].([]any)
	if b, _ := json.Marshal(msgs[1].(map[string]any)["content"]); !strings.Contains(string(b), "keep-me") {
		t.Errorf("earlier thinking block not sent back: %s", b)
	}
	if got["thinking"].(map[string]any)["type"] != "adaptive" || got["fallbacks"] != "default" ||
		got["output_config"].(map[string]any)["effort"] != "medium" || got["cache_control"] == nil || got["stream"] != true {
		t.Errorf("request %v", got)
	}
	if len(got["tools"].([]any)) != len(Specs) {
		t.Errorf("tools %v", got["tools"])
	}
}
