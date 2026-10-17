// Package agent runs Investigations: an analyst's conversation with the
// assistant, which reasons over several steps and looks things up with
// read-only tools before it answers (ADR-0022). Everything said is stored,
// rewinds set messages aside instead of deleting them, and every number in
// an answer is checked against what was looked up.
package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/cases"
	"github.com/kanishka-sahoo/unfraud/internal/investigate"
	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
	"github.com/kanishka-sahoo/unfraud/internal/store"
)

// SystemPrompt is the assistant's standing instructions. It never changes
// within a conversation, which keeps it cached and earlier thinking valid.
const SystemPrompt = `You are the investigation assistant in a card fraud operations console. You help fraud analysts understand alerts: payments the fraud engine blocked or sent to review. Analysts are fraud specialists, not developers or data scientists.

How to work:
- Look things up before answering. Use several lookups when a question needs them: an alert's details, the card's history, the merchant's activity, other cards flagged nearby, the card's network. Stop when you have enough; do not look up what you won't use.
- When the analyst opened you from an alert or a card, the first message tells you which; "this card" and "this alert" mean those.
- Lookups are about dataset time: the payments come from a public dataset with reconstructed dates, not today's date. Windows end at the time being investigated unless you say otherwise.
- Some questions can't be answered from what the console knows (for example, who the cardholder is or whether they called the bank). Say so plainly and suggest what the analyst could check instead.

How to answer:
- Lead with the answer in one or two sentences, then the supporting facts. Keep it short: most answers fit in under 150 words.
- Write for an analyst: cards, merchants, payments, alerts and outcomes. Never mention tools, JSON, field names, feature names, SHAP, log-odds or model internals; describe model factors by what they mean.
- Quote numbers exactly as the lookups gave them, with amounts in dollars ($1,631.26). Don't compute new totals, differences or ratios. Write card, merchant and alert IDs exactly as given, so the console can link them.
- Name a fraud pattern (card testing, a compromised merchant, a burst of spending, cash-out) only when specific facts show it; say which facts, and say they are consistent with it, never certain.
- Analyst outcomes (confirmed fraud, dismissed, escalated) are other analysts' judgments; "confirmed fraud" in the network means a confirmed fraud label. Keep the two apart.
- You only read. You can't change alerts or record outcomes, and you never tell the analyst whether to confirm or dismiss an alert: lay out the evidence, and the analyst decides.
- Format: short paragraphs, and "- " bullet lists for several facts. Use **bold** sparingly. No headings, tables or code.`

// MaxLookups bounds the lookups per question.
const MaxLookups = 8

// Agent answers questions in Investigations.
type Agent struct {
	Store  *store.Store
	Lookup investigate.Lookup
	Model  Model
	// Budget is the most tokens an Investigation may use; 0 is no limit.
	Budget int64
	// OnTurn, if set, sees every finished turn (for metrics).
	OnTurn func(Turn)

	mu   sync.Mutex
	busy map[string]bool
}

// ErrBusy means the Investigation is already answering a question.
var ErrBusy = errors.New("still answering the previous question")

// ErrBudget means the Investigation has used its token budget.
var ErrBudget = errors.New("this conversation has reached its length limit; start a new chat to keep going")

// Event is one server-sent event while a question is answered.
type Event struct {
	Type    string `json:"type"` // question, step, text, done, error
	Seq     int    `json:"seq,omitempty"`
	Step    *Step  `json:"step,omitempty"`
	Text    string `json:"text,omitempty"`
	Turn    *Turn  `json:"turn,omitempty"`
	Message string `json:"message,omitempty"`
}

// opening is what the assistant is told about the page it was opened on.
type opening struct {
	Page       string          `json:"page"` // alert, card, console
	AlertID    string          `json:"alert_id,omitempty"`
	Card       string          `json:"card,omitempty"`
	DatasetNow string          `json:"time_being_investigated"`
	Alert      *cases.Packet   `json:"alert,omitempty"`
	Summary    json.RawMessage `json:"existing_summary,omitempty"`
	Card30d    *cases.History  `json:"card_history,omitempty"`
}

// Create opens an Investigation on an alert, a card, or neither.
func (a *Agent) Create(ctx context.Context, profile, caseID, card string) (store.Investigation, error) {
	p, err := registry.Get(profile)
	if err != nil {
		return store.Investigation{}, fmt.Errorf("%w: %v", errInput, err)
	}
	o := opening{Page: "console"}
	inv := store.Investigation{Profile: profile}
	switch {
	case caseID != "":
		c, err := a.Store.GetCase(ctx, store.CaseKey{Profile: profile, EventID: caseID})
		if err != nil {
			return inv, err
		}
		o.Page, o.AlertID, o.Card = "alert", caseID, c.Entities[investigate.Card(p)]
		o.DatasetNow = investigate.When(c.EventTime)
		if pk, err := a.Lookup.Packet(ctx, c); err == nil {
			o.Alert = &pk
		}
		if c.SummaryStatus == "done" {
			o.Summary = summaryOnly(c.Summary)
		}
		inv.CaseID = &caseID
		if o.Card != "" {
			inv.Card = &o.Card
		}
	case card != "":
		now, err := a.Store.LatestEventTime(ctx, profile)
		if err != nil {
			return inv, err
		}
		o.Page, o.Card, o.DatasetNow = "card", card, investigate.When(now)
		if h, _, err := a.Lookup.CardHistory(ctx, p, card, now, investigate.HistoryDays); err == nil {
			o.Card30d = h
		}
		inv.Card = &card
	default:
		now, err := a.Store.LatestEventTime(ctx, profile)
		if err != nil {
			return inv, err
		}
		o.DatasetNow = investigate.When(now)
	}
	if inv.Context, err = json.Marshal(o); err != nil {
		return inv, err
	}
	return a.Store.CreateInvestigation(ctx, inv)
}

// now is the dataset time an Investigation's lookups end at by default.
func now(inv store.Investigation) time.Time {
	var o opening
	json.Unmarshal(inv.Context, &o)
	t, _ := time.Parse("2006-01-02 15:04", o.DatasetNow)
	// Lookups end just after the minute the alert is shown at.
	return t.Add(time.Minute - time.Second)
}

// contextBlock introduces the conversation: what page it was opened on.
func contextBlock(inv store.Investigation) map[string]any {
	var o opening
	json.Unmarshal(inv.Context, &o)
	intro := "The analyst opened the assistant from the console's overview."
	switch o.Page {
	case "alert":
		intro = "The analyst opened the assistant on alert " + o.AlertID + ". Here is what is known about it."
	case "card":
		intro = "The analyst opened the assistant on card " + o.Card + "'s network page. Here is the card's recent alert history."
	}
	return map[string]any{"type": "text", "text": intro + "\n<context>\n" + string(inv.Context) + "\n</context>"}
}

// Ask answers a question, streaming its progress to emit. fromSeq > 0
// rewinds first: the messages from fromSeq on are set aside and the
// question replaces them (edit and resend, or retry).
func (a *Agent) Ask(ctx context.Context, inv store.Investigation, question string, fromSeq int, emit func(Event)) error {
	question = strings.TrimSpace(question)
	if question == "" {
		return fmt.Errorf("%w: ask a question", errInput)
	}
	a.mu.Lock()
	if a.busy == nil {
		a.busy = map[string]bool{}
	}
	if a.busy[inv.ID] {
		a.mu.Unlock()
		return ErrBusy
	}
	a.busy[inv.ID] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.busy, inv.ID)
		a.mu.Unlock()
	}()

	if a.Budget > 0 {
		used, err := a.Store.InvestigationTokens(ctx, inv.ID)
		if err != nil {
			return err
		}
		if used >= a.Budget {
			return ErrBudget
		}
	}
	if fromSeq > 0 {
		if err := a.Store.Rewind(ctx, inv.ID, fromSeq); err != nil {
			return err
		}
	}
	q, _ := json.Marshal([]map[string]any{{"type": "text", "text": question}})
	saved, err := a.Store.AppendMessages(ctx, inv.ID, store.Message{Role: "user", Kind: "question", Content: q})
	if err != nil {
		return err
	}
	emit(Event{Type: "question", Seq: saved[0].Seq})

	tools, err := NewTools(ctx, a.Lookup, inv, now(inv))
	if err != nil {
		return err
	}
	// Writes after the analyst presses Stop must still land.
	keep := context.WithoutCancel(ctx)
	lookups := 0
	for {
		history, err := a.Store.Messages(keep, inv.ID, false)
		if err != nil {
			return err
		}
		req := Request{System: SystemPrompt, Tools: Specs, Messages: wire(inv, history)}
		reply, err := a.Model.Reply(ctx, req, func(t string) { emit(Event{Type: "text", Text: t}) })
		if err != nil {
			kind, text := "stopped", "Stopped."
			if ctx.Err() == nil {
				text = "Sorry, something went wrong while answering. Please try again."
			}
			meta, _ := json.Marshal(map[string]any{"error": err.Error(), "stopped": ctx.Err() != nil})
			c, _ := json.Marshal([]map[string]any{{"type": "text", "text": text}})
			a.Store.AppendMessages(keep, inv.ID, store.Message{Role: "assistant", Kind: kind, Content: c, Meta: meta})
			if ctx.Err() == nil {
				emit(Event{Type: "error", Message: text})
			}
			return a.finish(keep, inv, emit)
		}
		var blocks []block
		if err := json.Unmarshal(reply.Content, &blocks); err != nil {
			return err
		}
		meta := map[string]any{"model": reply.Model, "stop_reason": reply.StopReason, "cost_usd": reply.CostUSD,
			"input_tokens":  reply.Usage.InputTokens + reply.Usage.CacheReadTokens + reply.Usage.CacheWriteTokens,
			"output_tokens": reply.Usage.OutputTokens}
		var calls []block
		for _, b := range blocks {
			if b.Type == "tool_use" {
				calls = append(calls, b)
			}
		}
		switch {
		case reply.StopReason == "tool_use" && len(calls) > 0:
			for _, c := range calls {
				emit(Event{Type: "step", Step: &Step{ID: c.ID, Tool: c.Name, Label: Label(c.Name, c.Input), Status: "running"}})
			}
			results, display := a.run(ctx, tools, calls, &lookups, emit)
			rc, _ := json.Marshal(results)
			dc, _ := json.Marshal(display)
			mc, _ := json.Marshal(meta)
			if _, err := a.Store.AppendMessages(keep, inv.ID,
				store.Message{Role: "assistant", Kind: "reply", Content: reply.Content, Meta: mc},
				store.Message{Role: "user", Kind: "tool_results", Content: rc, Display: dc}); err != nil {
				return err
			}
			if ctx.Err() != nil {
				c, _ := json.Marshal([]map[string]any{{"type": "text", "text": "Stopped."}})
				m, _ := json.Marshal(map[string]any{"stopped": true})
				a.Store.AppendMessages(keep, inv.ID, store.Message{Role: "assistant", Kind: "stopped", Content: c, Meta: m})
				return a.finish(keep, inv, emit)
			}
			continue
		case reply.StopReason == "refusal":
			meta["refused"] = true
			text := "I can't help with that request. Try asking about the alert's payment, the card or the merchant."
			content, _ := json.Marshal([]map[string]any{{"type": "text", "text": text}})
			mc, _ := json.Marshal(meta)
			a.Store.AppendMessages(keep, inv.ID, store.Message{Role: "assistant", Kind: "stopped", Content: content, Meta: mc})
			return a.finish(keep, inv, emit)
		}
		// The answer. A reply cut off at max_tokens keeps only its text, so
		// no half-written lookup is stored.
		if reply.StopReason == "max_tokens" {
			var text []map[string]any
			for _, b := range blocks {
				if b.Type == "text" {
					text = append(text, map[string]any{"type": "text", "text": b.Text})
				}
			}
			text = append(text, map[string]any{"type": "text", "text": "(The answer was cut off because it ran too long.)"})
			reply.Content, _ = json.Marshal(text)
			meta["truncated"] = true
		}
		answer := textOf(blocks)
		meta["unverified"] = a.unverified(answer, inv, history, question)
		mc, _ := json.Marshal(meta)
		if _, err := a.Store.AppendMessages(keep, inv.ID, store.Message{Role: "assistant", Kind: "reply", Content: reply.Content, Meta: mc}); err != nil {
			return err
		}
		return a.finish(keep, inv, emit)
	}
}

// finish sends the turn as stored, which the console shows from now on.
func (a *Agent) finish(ctx context.Context, inv store.Investigation, emit func(Event)) error {
	ms, err := a.Store.Messages(ctx, inv.ID, false)
	if err != nil {
		return err
	}
	turns := Render(inv, ms)
	if len(turns) > 0 {
		t := turns[len(turns)-1]
		if a.OnTurn != nil {
			a.OnTurn(t)
		}
		emit(Event{Type: "done", Turn: &t})
	}
	return nil
}

// run performs a reply's lookups, in parallel, and returns the tool_result
// blocks and what the console shows for each.
func (a *Agent) run(ctx context.Context, tools *Tools, calls []block, lookups *int, emit func(Event)) ([]map[string]any, []Step) {
	results := make([]map[string]any, len(calls))
	steps := make([]Step, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		*lookups++
		over := *lookups > MaxLookups
		wg.Add(1)
		go func() {
			defer wg.Done()
			step := Step{ID: c.ID, Tool: c.Name, Label: Label(c.Name, c.Input)}
			var out any
			var err error
			switch {
			case over:
				err = fmt.Errorf("%w: the lookup limit for this question is reached; answer with what you have and say what is left to check", errInput)
			case ctx.Err() != nil:
				err = errors.New("stopped")
			default:
				var s Step
				out, s, err = tools.Run(ctx, c.Name, c.Input)
				step.Summary, step.Table = s.Summary, s.Table
			}
			var content string
			if err != nil {
				step.Status = "error"
				step.Error = plainError(err)
				if over {
					step.Error = "Skipped: this question has used its lookups"
				}
				content = strings.TrimPrefix(err.Error(), errInput.Error()+": ")
			} else {
				step.Status = "done"
				b, _ := json.Marshal(map[string]any{"step_summary": step.Summary, "data": out})
				content = string(b)
			}
			results[i] = map[string]any{"type": "tool_result", "tool_use_id": c.ID, "content": content, "is_error": err != nil}
			steps[i] = step
			emit(Event{Type: "step", Step: &step})
		}()
	}
	wg.Wait()
	return results, steps
}

// plainError says what went wrong without internals.
func plainError(err error) string {
	switch {
	case errors.Is(err, errInput):
		msg := strings.TrimPrefix(err.Error(), errInput.Error()+": ")
		return strings.ToUpper(msg[:1]) + msg[1:]
	case strings.Contains(err.Error(), "network data"):
		return "The network data is unavailable right now"
	case err.Error() == "stopped":
		return "Stopped"
	}
	return "This lookup failed"
}

// unverified lists the numbers and IDs in an answer that nothing looked up
// in the conversation (nor the analyst's own questions) contains.
func (a *Agent) unverified(answer string, inv store.Investigation, history []store.Message, question string) []string {
	corpus := []json.RawMessage{inv.Context, mustJSON(question)}
	for _, m := range history {
		if m.Kind == "tool_results" || m.Kind == "question" {
			corpus = append(corpus, m.Content)
		}
	}
	var source strings.Builder
	for _, c := range corpus {
		source.Write(c)
		source.WriteByte('\n')
	}
	// Tool results nest JSON in strings; unescape so IDs match verbatim.
	text := strings.ReplaceAll(source.String(), `\"`, `"`)
	var out []string
	for _, p := range cases.GroundText(answer, text) {
		_, tok, _ := strings.Cut(p, " ")
		if !slices.Contains(out, tok) {
			out = append(out, tok)
		}
	}
	return out
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// wire turns stored messages into the API's messages, with the opening
// context ahead of the first question.
func wire(inv store.Investigation, ms []store.Message) []Wire {
	out := make([]Wire, 0, len(ms))
	for i, m := range ms {
		content := m.Content
		if i == 0 {
			var blocks []json.RawMessage
			json.Unmarshal(m.Content, &blocks)
			ctxBlock, _ := json.Marshal(contextBlock(inv))
			content, _ = json.Marshal(append([]json.RawMessage{ctxBlock}, blocks...))
		}
		out = append(out, Wire{Role: m.Role, Content: content})
	}
	return out
}

func textOf(blocks []block) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, strings.TrimSpace(b.Text))
		}
	}
	return strings.Join(parts, "\n\n")
}

// Turn is one question and everything the assistant did to answer it, as
// the console shows it.
type Turn struct {
	Seq      int       `json:"seq"`
	Question string    `json:"question"`
	AskedAt  time.Time `json:"asked_at"`
	Steps    []Step    `json:"steps"`
	Answer   string    `json:"answer"`
	// Status is answered, stopped, error, refused or pending (no reply yet).
	Status     string            `json:"status"`
	Unverified []string          `json:"unverified"`
	Links      map[string]string `json:"links"`
	Model      string            `json:"model,omitempty"`
	CostUSD    float64           `json:"cost_usd"`
	Tokens     int64             `json:"tokens"`
}

var linkable = regexp.MustCompile(`^(/alerts/|/network/)`)

// openingLinks links the alerts and cards the assistant was told about
// when the Investigation opened.
func openingLinks(inv store.Investigation) map[string]string {
	var o opening
	json.Unmarshal(inv.Context, &o)
	links := map[string]string{}
	add := func(id, href string) {
		if id != "" {
			links[id] = href
		}
	}
	add(o.AlertID, alertHref(o.AlertID))
	add(o.Card, cardHref(o.Card))
	if pk := o.Alert; pk != nil {
		if pk.Earlier != nil {
			for _, e := range pk.Earlier.Alerts {
				add(e.AlertID, alertHref(e.AlertID))
			}
		}
		if pk.Related != nil {
			for _, r := range pk.Related.Alerts {
				add(r.AlertID, alertHref(r.AlertID))
				add(r.Card, cardHref(r.Card))
			}
		}
		if pk.Network != nil {
			for _, f := range pk.Network.FraudLinks {
				add(f.Card, cardHref(f.Card))
			}
		}
	}
	return links
}

// Render groups an Investigation's messages into turns.
func Render(inv store.Investigation, ms []store.Message) []Turn {
	base := openingLinks(inv)
	var turns []Turn
	for _, m := range ms {
		var blocks []block
		json.Unmarshal(m.Content, &blocks)
		if m.Kind == "question" {
			links := make(map[string]string, len(base))
			for k, v := range base {
				links[k] = v
			}
			turns = append(turns, Turn{Seq: m.Seq, Question: textOf(blocks), AskedAt: m.CreatedAt, Status: "pending",
				Steps: []Step{}, Unverified: []string{}, Links: links})
			continue
		}
		if len(turns) == 0 {
			continue
		}
		t := &turns[len(turns)-1]
		var meta struct {
			Model        string   `json:"model"`
			CostUSD      float64  `json:"cost_usd"`
			InputTokens  int64    `json:"input_tokens"`
			OutputTokens int64    `json:"output_tokens"`
			Unverified   []string `json:"unverified"`
			Refused      bool     `json:"refused"`
			Stopped      bool     `json:"stopped"`
			Error        string   `json:"error"`
		}
		json.Unmarshal(m.Meta, &meta)
		t.CostUSD += meta.CostUSD
		t.Tokens += meta.InputTokens + meta.OutputTokens
		t.Model = cmp.Or(meta.Model, t.Model)
		switch m.Kind {
		case "reply":
			hasCalls := slices.ContainsFunc(blocks, func(b block) bool { return b.Type == "tool_use" })
			for _, b := range blocks {
				switch {
				case b.Type == "text" && hasCalls && strings.TrimSpace(b.Text) != "":
					t.Steps = append(t.Steps, Step{ID: fmt.Sprintf("note-%d", m.Seq), Status: "note", Note: strings.TrimSpace(b.Text)})
				case b.Type == "tool_use":
					t.Steps = append(t.Steps, Step{ID: b.ID, Tool: b.Name, Label: Label(b.Name, b.Input), Status: "running"})
				}
			}
			if !hasCalls {
				t.Answer, t.Status = textOf(blocks), "answered"
				t.Unverified = append(t.Unverified[:0], meta.Unverified...)
			}
		case "tool_results":
			var steps []Step
			json.Unmarshal(m.Display, &steps)
			for _, s := range steps {
				for i := range t.Steps {
					if t.Steps[i].ID == s.ID {
						t.Steps[i] = s
					}
				}
				if s.Table != nil {
					for _, row := range s.Table.Rows {
						for _, c := range row {
							if c.Href != "" && linkable.MatchString(c.Href) {
								t.Links[c.Text] = c.Href
							}
						}
					}
				}
			}
		case "stopped":
			t.Answer = textOf(blocks)
			switch {
			case meta.Refused:
				t.Status = "refused"
			case meta.Stopped:
				t.Status = "stopped"
			default:
				t.Status = "error"
			}
			for i := range t.Steps {
				if t.Steps[i].Status == "running" {
					t.Steps[i].Status = "error"
					t.Steps[i].Error = "Stopped"
				}
			}
		}
	}
	if turns == nil {
		turns = []Turn{}
	}
	return turns
}

// Suggestions are questions that fit the page the assistant was opened on.
func Suggestions(inv store.Investigation) []string {
	var o opening
	json.Unmarshal(inv.Context, &o)
	switch o.Page {
	case "alert":
		return []string{
			"Has this card been flagged before?",
			"Is this merchant seeing other fraud?",
			"Who else is connected to this card?",
			"What should I check before deciding?",
		}
	case "card":
		return []string{
			"What does this card's recent activity look like?",
			"Are any cards near this one confirmed fraud?",
			"Which merchants link this card to others?",
		}
	}
	return []string{
		"Which open blocked alerts have the highest fraud probability?",
		"Which merchants have the most alerts this week?",
		"Show alerts that analysts confirmed as fraud in the last 7 days",
	}
}
