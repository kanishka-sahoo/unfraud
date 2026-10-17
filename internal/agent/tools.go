package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/cases"
	"github.com/kanishka-sahoo/unfraud/internal/investigate"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
	"github.com/kanishka-sahoo/unfraud/internal/store"
)

// ToolSpec is a tool as the model sees it.
type ToolSpec struct {
	Name        string
	Description string
	Properties  map[string]any
	Required    []string
}

// Step is what the console shows for one lookup: a sentence while it runs,
// another when it is done, and its data as a table.
type Step struct {
	ID      string `json:"id"`
	Tool    string `json:"tool,omitempty"`
	Label   string `json:"label,omitempty"` // "Looking at card 77-1's alerts in the last 30 days"
	Status  string `json:"status"`          // running, done, error, note
	Summary string `json:"summary,omitempty"`
	Error   string `json:"error,omitempty"`
	Table   *Table `json:"table,omitempty"`
	// Note is something the assistant said between lookups.
	Note string `json:"note,omitempty"`
}

type Table struct {
	Columns []string `json:"columns"`
	Rows    [][]Cell `json:"rows"`
	// More says how many rows were left out.
	More int `json:"more,omitempty"`
}

type Cell struct {
	Text string `json:"text"`
	Href string `json:"href,omitempty"`
	Tone string `json:"tone,omitempty"` // high, elevated, low
}

// Specs are the assistant's tools. They only read.
var Specs = []ToolSpec{
	{
		Name: "get_alert",
		Description: "Everything known about one alert (a flagged payment): the payment, the decision and fraud probability from each stage, " +
			"the factors behind the score, the card's alerts in the 72 hours before and 30 days up to it, the merchant's alerts in the 7 days " +
			"up to it, other cards' alerts at the merchant within 24 hours, the card's network, and any analyst outcome.",
		Properties: map[string]any{"alert_id": str("The alert ID, such as 10214163.")},
		Required:   []string{"alert_id"},
	},
	{
		Name: "search_alerts",
		Description: "Find alerts by card, merchant, merchant category code, decision, analyst outcome and time. Returns how many match " +
			"and the newest ones (at most 25). Times are dataset times, written YYYY-MM-DD or YYYY-MM-DD HH:MM.",
		Properties: map[string]any{
			"card":     str("Card ID, such as 77-1."),
			"merchant": str("Merchant ID."),
			"mcc":      str("Merchant category code, such as 5411."),
			"decision": enum("Only alerts with this decision.", "review", "block"),
			"outcome":  enum("Only alerts with this analyst outcome; open means not yet reviewed.", "open", "confirmed", "dismissed", "escalated"),
			"from":     str("Earliest dataset time, inclusive."),
			"to":       str("Latest dataset time, exclusive."),
			"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": 25, "description": "How many alerts to list (default 10)."},
		},
	},
	{
		Name: "card_history",
		Description: "A card's alerts over a window ending at the time being investigated: how many, how many blocked, analyst outcomes, " +
			"total amount, alerts by merchant type, and the newest alerts (at most 25).",
		Properties: map[string]any{
			"card":  str("Card ID."),
			"days":  map[string]any{"type": "integer", "minimum": 1, "maximum": 180, "description": "Window length in days (default 30)."},
			"until": str("End of the window as a dataset time; defaults to the time being investigated."),
		},
		Required: []string{"card"},
	},
	{
		Name: "merchant_activity",
		Description: "A merchant's alerts on any card over a window ending at the time being investigated: how many, how many cards, " +
			"blocked, analyst outcomes, and the newest alerts (at most 25). Many cards flagged at one merchant can point to a compromised merchant.",
		Properties: map[string]any{
			"merchant": str("Merchant ID."),
			"days":     map[string]any{"type": "integer", "minimum": 1, "maximum": 90, "description": "Window length in days (default 7)."},
			"until":    str("End of the window as a dataset time; defaults to the time being investigated."),
		},
		Required: []string{"merchant"},
	},
	{
		Name: "card_network",
		Description: "The network around a card in the graph as it is now (which can be long after the alert being investigated): the " +
			"merchants it used in the 30 days before then, the other cards that used them, which of those have confirmed fraud and which " +
			"merchants they share with the card. Merchants used by very many cards are not linked through.",
		Properties: map[string]any{
			"card": str("Card ID."),
			"hops": map[string]any{"type": "integer", "minimum": 1, "maximum": 4,
				"description": "How far to walk: 2 (default) reaches cards sharing a merchant, 4 reaches their merchants' cards."},
		},
		Required: []string{"card"},
	},
	{
		Name:        "related_alerts",
		Description: "Other cards' alerts at the same merchant as an alert, within some hours either side of it, nearest first.",
		Properties: map[string]any{
			"alert_id": str("The alert ID."),
			"hours":    map[string]any{"type": "integer", "minimum": 1, "maximum": 168, "description": "Hours either side (default 24)."},
		},
		Required: []string{"alert_id"},
	},
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func enum(desc string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "enum": vals, "description": desc}
}

// Tools runs the assistant's lookups for one Investigation.
type Tools struct {
	Lookup  investigate.Lookup
	Profile profile.Profile
	// Now is the time windows end at by default: the alert's time, or the
	// newest alert's.
	Now time.Time
}

// NewTools prepares the lookups for an Investigation.
func NewTools(ctx context.Context, l investigate.Lookup, inv store.Investigation, now time.Time) (*Tools, error) {
	p, err := registry.Get(inv.Profile)
	if err != nil {
		return nil, err
	}
	return &Tools{Lookup: l, Profile: p, Now: now}, nil
}

type toolInput struct {
	AlertID  string `json:"alert_id"`
	Card     string `json:"card"`
	Merchant string `json:"merchant"`
	MCC      string `json:"mcc"`
	Decision string `json:"decision"`
	Outcome  string `json:"outcome"`
	From     string `json:"from"`
	To       string `json:"to"`
	Until    string `json:"until"`
	Limit    int    `json:"limit"`
	Days     int    `json:"days"`
	Hours    int    `json:"hours"`
	Hops     int    `json:"hops"`
}

// errInput is a mistake in the model's input; it is told, not the analyst.
var errInput = errors.New("invalid input")

// Label says in plain words what a lookup is about to do.
func Label(tool string, raw json.RawMessage) string {
	var in toolInput
	json.Unmarshal(raw, &in)
	switch tool {
	case "get_alert":
		return "Opening alert " + in.AlertID
	case "search_alerts":
		var parts []string
		if in.Card != "" {
			parts = append(parts, "on card "+in.Card)
		}
		if in.Merchant != "" {
			parts = append(parts, "at merchant "+in.Merchant)
		}
		if in.MCC != "" {
			parts = append(parts, "at merchant category "+in.MCC)
		}
		if in.Decision != "" {
			parts = append(parts, map[string]string{"block": "that were blocked", "review": "sent to review"}[in.Decision])
		}
		if in.Outcome != "" {
			parts = append(parts, map[string]string{"open": "not yet reviewed", "confirmed": "confirmed as fraud",
				"dismissed": "dismissed", "escalated": "escalated"}[in.Outcome])
		}
		return strings.TrimSpace("Searching alerts " + strings.Join(parts, ", "))
	case "card_history":
		return fmt.Sprintf("Looking at card %s's alerts over %s", in.Card, investigate.Days(cmp.Or(in.Days, 30)))
	case "merchant_activity":
		return fmt.Sprintf("Looking at merchant %s's alerts over %s", in.Merchant, investigate.Days(cmp.Or(in.Days, 7)))
	case "card_network":
		return "Mapping the network around card " + in.Card
	case "related_alerts":
		return fmt.Sprintf("Finding other cards' alerts near alert %s at the same merchant", in.AlertID)
	}
	return "Looking something up"
}

// Run performs one lookup. It returns what the model reads and what the
// console shows; an error is a sentence for the analyst.
func (t *Tools) Run(ctx context.Context, tool string, raw json.RawMessage) (any, Step, error) {
	var in toolInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, Step{}, fmt.Errorf("%w: %v", errInput, err)
	}
	p := t.Profile
	switch tool {
	case "get_alert":
		c, err := t.Lookup.Store.GetCase(ctx, store.CaseKey{Profile: p.Name, EventID: strings.TrimSpace(in.AlertID)})
		if errors.Is(err, store.ErrNotFound) {
			return nil, Step{}, fmt.Errorf("%w: there is no alert %s", errInput, in.AlertID)
		} else if err != nil {
			return nil, Step{}, err
		}
		pk, err := t.Lookup.Packet(ctx, c)
		if err != nil {
			return nil, Step{}, err
		}
		out := struct {
			cases.Packet
			Card            string          `json:"card"`
			Merchant        string          `json:"merchant"`
			AnalystOutcome  string          `json:"analyst_outcome,omitempty"`
			AnalystNote     string          `json:"analyst_note,omitempty"`
			ExistingSummary json.RawMessage `json:"existing_summary,omitempty"`
		}{Packet: pk, Card: c.Entities[investigate.Card(p)], Merchant: c.Entities[investigate.Merchant(p)],
			AnalystOutcome: deref(c.Disposition), AnalystNote: deref(c.DispositionNote)}
		if c.SummaryStatus == "done" {
			out.ExistingSummary = summaryOnly(c.Summary)
		}
		return out, alertStep(p, c, pk), nil

	case "search_alerts":
		q := store.CaseSearch{Profile: p.Name, Entities: map[string]string{}, Disposition: in.Outcome, Limit: cmp.Or(in.Limit, 10)}
		if in.Card != "" {
			q.Entities[investigate.Card(p)] = in.Card
		}
		if in.Merchant != "" {
			q.Entities[investigate.Merchant(p)] = in.Merchant
		}
		switch in.Decision {
		case "block":
			q.MinAction = int16(profile.Block)
		case "review", "":
		default:
			return nil, Step{}, fmt.Errorf("%w: decision is review or block", errInput)
		}
		var err error
		if q.From, err = parseTime(in.From, time.Time{}); err != nil {
			return nil, Step{}, err
		}
		if q.To, err = parseTime(in.To, time.Time{}); err != nil {
			return nil, Step{}, err
		}
		if in.MCC != "" {
			// The store has no MCC index; read a wider page and filter.
			q.Limit = 500
		}
		cs, total, err := t.Lookup.Store.FindCases(ctx, q)
		if err != nil {
			return nil, Step{}, err
		}
		if in.MCC != "" {
			var keep []store.CaseListItem
			for _, c := range cs {
				if fmt.Sprint(c.Values["mcc"]) == in.MCC {
					keep = append(keep, c)
				}
			}
			cs, total = keep, len(keep)
			if total == 500 {
				total = -1 // unknown beyond the page read
			}
		}
		lim := min(cmp.Or(in.Limit, 10), 25)
		shown := cs[:min(lim, len(cs))]
		out := map[string]any{"matching_alerts": total, "alerts": t.alertRows(shown)}
		if total < 0 {
			out["matching_alerts"] = "500 or more"
		}
		summary := fmt.Sprintf("%d matching alerts", total)
		switch {
		case total < 0:
			summary = "500 or more matching alerts"
		case total == 0:
			summary = "No matching alerts"
		case total == 1:
			summary = "1 matching alert"
		}
		return out, Step{Summary: summary, Table: t.alertTable(shown, total-len(shown))}, nil

	case "card_history":
		if in.Card == "" {
			return nil, Step{}, fmt.Errorf("%w: card is required", errInput)
		}
		until, err := parseTime(in.Until, t.Now)
		if err != nil {
			return nil, Step{}, err
		}
		days := min(cmp.Or(in.Days, 30), 180)
		h, cs, err := t.Lookup.CardHistory(ctx, p, in.Card, until, days)
		if err != nil {
			return nil, Step{}, err
		}
		shown := cs[:min(25, len(cs))]
		out := map[string]any{"history": h, "newest_alerts": t.alertRows(shown)}
		summary := fmt.Sprintf("%s in %s, %d blocked, %d confirmed as fraud", plural(h.Alerts, "alert"), h.Window, h.Blocked, h.Confirmed)
		if h.Alerts == 0 {
			summary = "No alerts on this card in " + h.Window
		}
		return out, Step{Summary: summary, Table: t.alertTable(shown, h.Alerts-len(shown))}, nil

	case "merchant_activity":
		if in.Merchant == "" {
			return nil, Step{}, fmt.Errorf("%w: merchant is required", errInput)
		}
		until, err := parseTime(in.Until, t.Now)
		if err != nil {
			return nil, Step{}, err
		}
		days := min(cmp.Or(in.Days, 7), 90)
		m, cs, err := t.Lookup.MerchantActivity(ctx, p, in.Merchant, until, days)
		if err != nil {
			return nil, Step{}, err
		}
		shown := cs[:min(25, len(cs))]
		out := map[string]any{"activity": m, "newest_alerts": t.alertRows(shown)}
		summary := fmt.Sprintf("%s on %s in %s, %d confirmed as fraud", plural(m.Alerts, "alert"), plural(m.Cards, "card"), m.Window, m.Confirmed)
		if m.Alerts == 0 {
			summary = "No alerts at this merchant in " + m.Window
		}
		return out, Step{Summary: summary, Table: t.alertTable(shown, m.Alerts-len(shown))}, nil

	case "card_network":
		if in.Card == "" {
			return nil, Step{}, fmt.Errorf("%w: card is required", errInput)
		}
		n, sg, err := t.Lookup.Network(ctx, p, in.Card, min(cmp.Or(in.Hops, 2), 4))
		if err != nil {
			return nil, Step{}, errors.New("the network data is unavailable right now")
		}
		var merchants []string
		for _, x := range sg.Nodes {
			if x.ID != sg.Center && x.Kind != investigate.Card(p) && len(merchants) < 40 {
				merchants = append(merchants, x.ID)
			}
		}
		tbl := &Table{Columns: []string{"Card with confirmed fraud", "How it connects"}, More: n.FraudCards - len(n.FraudLinks)}
		for _, f := range n.FraudLinks {
			how := "Through another linked card"
			if len(f.SharedMerchants) > 0 {
				how = "Shares merchant " + strings.Join(f.SharedMerchants, ", ")
			}
			tbl.Rows = append(tbl.Rows, []Cell{{Text: f.Card, Href: cardHref(f.Card), Tone: "high"}, {Text: how}})
		}
		out := map[string]any{"network": n, "merchant_ids": merchants}
		summary := fmt.Sprintf("%s, %s, %d with confirmed fraud", plural(n.Merchants, "merchant"), plural(n.OtherCards, "other card"), n.FraudCards)
		if n.AsOf != "" {
			summary += " (graph as of " + n.AsOf + ")"
		}
		if len(tbl.Rows) == 0 {
			tbl = nil
		}
		return out, Step{Summary: summary, Table: tbl}, nil

	case "related_alerts":
		c, err := t.Lookup.Store.GetCase(ctx, store.CaseKey{Profile: p.Name, EventID: strings.TrimSpace(in.AlertID)})
		if errors.Is(err, store.ErrNotFound) {
			return nil, Step{}, fmt.Errorf("%w: there is no alert %s", errInput, in.AlertID)
		} else if err != nil {
			return nil, Step{}, err
		}
		merchant := c.Entities[investigate.Merchant(p)]
		if merchant == "" {
			return map[string]any{"related_alerts": nil, "note": "the alert has no merchant"}, Step{Summary: "The alert has no merchant"}, nil
		}
		r, err := t.Lookup.Related(ctx, p, c.EventID, merchant, c.Entities[investigate.Card(p)], c.EventTime, min(cmp.Or(in.Hours, 24), 168))
		if err != nil {
			return nil, Step{}, err
		}
		tbl := &Table{Columns: []string{"Time", "Alert", "Card", "Amount", "Decision", "Probability", "Outcome"}, More: r.Count - len(r.Alerts)}
		for _, a := range r.Alerts {
			tbl.Rows = append(tbl.Rows, []Cell{{Text: a.EventTime}, {Text: a.AlertID, Href: alertHref(a.AlertID)},
				{Text: a.Card, Href: cardHref(a.Card)}, {Text: money(a.AmountUSD)}, {Text: a.Decision, Tone: tone(a.Decision)},
				{Text: a.Percent}, {Text: outcome(a.Disposition)}})
		}
		summary := fmt.Sprintf("%s on %s within %s", plural(r.Count, "other alert"), plural(r.Cards, "card"), r.Window)
		if r.Count == 0 {
			summary = "No other cards were flagged at this merchant within " + r.Window
			tbl = nil
		}
		return map[string]any{"related_alerts": r}, Step{Summary: summary, Table: tbl}, nil
	}
	return nil, Step{}, fmt.Errorf("%w: unknown tool %s", errInput, tool)
}

// alertRow is an alert as the model reads it in a list.
type alertRow struct {
	AlertID      string `json:"alert_id"`
	EventTime    string `json:"event_time"`
	Card         string `json:"card"`
	Merchant     string `json:"merchant"`
	AmountUSD    any    `json:"amount_usd"`
	MerchantType string `json:"merchant_type,omitempty"`
	MCC          any    `json:"mcc,omitempty"`
	Decision     string `json:"decision"`
	Percent      string `json:"fraud_probability_percent"`
	Outcome      string `json:"analyst_outcome,omitempty"`
}

func (t *Tools) alertRows(cs []store.CaseListItem) []alertRow {
	out := []alertRow{}
	for _, c := range cs {
		out = append(out, alertRow{AlertID: c.EventID, EventTime: investigate.When(c.EventTime), Card: c.Entities[investigate.Card(t.Profile)],
			Merchant: c.Entities[investigate.Merchant(t.Profile)], AmountUSD: cases.RoundAny(c.Values["amount_usd"]),
			MerchantType: t.Profile.Meaning("mcc", c.Values["mcc"]), MCC: c.Values["mcc"],
			Decision: profile.Action(c.Action).String(), Percent: cases.Percent(float64(c.Probability)), Outcome: deref(c.Disposition)})
	}
	return out
}

func (t *Tools) alertTable(cs []store.CaseListItem, more int) *Table {
	if len(cs) == 0 {
		return nil
	}
	tbl := &Table{Columns: []string{"Time", "Alert", "Card", "Amount", "Merchant type", "Decision", "Probability", "Outcome"}, More: max(more, 0)}
	for _, r := range t.alertRows(cs) {
		tbl.Rows = append(tbl.Rows, []Cell{{Text: r.EventTime}, {Text: r.AlertID, Href: alertHref(r.AlertID)},
			{Text: r.Card, Href: cardHref(r.Card)}, {Text: money(r.AmountUSD)}, {Text: cmp.Or(r.MerchantType, fmt.Sprint(r.MCC))},
			{Text: r.Decision, Tone: tone(r.Decision)}, {Text: r.Percent}, {Text: outcome(r.Outcome)}})
	}
	return tbl
}

func alertStep(p profile.Profile, c store.Case, pk cases.Packet) Step {
	card, merchant := c.Entities[investigate.Card(p)], c.Entities[investigate.Merchant(p)]
	facts := map[string]cases.Fact{}
	for _, f := range pk.Payment {
		facts[f.Feature] = f
	}
	what := money(facts["amount_usd"].Value)
	if m := facts["mcc"].Meaning; m != "" {
		what += " at " + m
	}
	tbl := &Table{Columns: []string{"", ""}}
	row := func(k string, v Cell) { tbl.Rows = append(tbl.Rows, []Cell{{Text: k}, v}) }
	row("Alert", Cell{Text: c.EventID, Href: alertHref(c.EventID)})
	row("Payment", Cell{Text: what})
	row("Time", Cell{Text: investigate.When(c.EventTime) + " (dataset time)"})
	row("Decision", Cell{Text: pk.Decision.Action + ", " + pk.Decision.Percent + " fraud probability", Tone: tone(pk.Decision.Action)})
	if card != "" {
		row("Card", Cell{Text: card, Href: cardHref(card)})
	}
	if merchant != "" {
		row("Merchant", Cell{Text: merchant})
	}
	row("Analyst outcome", Cell{Text: outcome(deref(c.Disposition))})
	verb := map[string]string{"block": "blocked", "review": "sent to review", "approve": "approved"}[pk.Decision.Action]
	return Step{Summary: fmt.Sprintf("%s, %s with a %s fraud probability", what, cmp.Or(verb, pk.Decision.Action), pk.Decision.Percent), Table: tbl}
}

// summaryOnly keeps a stored Summary's text, not the Packet it carries.
func summaryOnly(raw json.RawMessage) json.RawMessage {
	var s cases.Summary
	if json.Unmarshal(raw, &s) != nil || s.Text == "" {
		return nil
	}
	b, _ := json.Marshal(s)
	return b
}

func parseTime(s string, def time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: %q is not a time like 2019-03-04 or 2019-03-04 15:30", errInput, s)
}

func alertHref(id string) string { return "/alerts/" + url.PathEscape(id) }
func cardHref(id string) string  { return "/network/" + url.PathEscape(id) }

func money(v any) string {
	f, ok := v.(float64)
	if !ok {
		return "–"
	}
	neg := f < 0
	s := fmt.Sprintf("%.2f", max(f, -f))
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := "$" + b.String() + "." + frac
	if neg {
		out = "-" + out
	}
	return out
}

func tone(decision string) string {
	return map[string]string{"block": "high", "review": "elevated", "approve": "low"}[decision]
}

func outcome(d string) string {
	return cmp.Or(map[string]string{"confirmed": "Confirmed fraud", "dismissed": "Dismissed", "escalated": "Escalated"}[d], "Not reviewed")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
