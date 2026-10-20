// Package llm writes Case Summaries through a pluggable driver: a stub that
// needs no API key, Claude, or any OpenAI-compatible API (ADR-0009, ADR-0021).
package llm

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kanishka-sahoo/unfraud/internal/cases"
)

// Meta records how a Summary was produced, for reproducibility and cost.
type Meta struct {
	Driver           string  `json:"driver"`
	Model            string  `json:"model"`    // as reported by the provider
	Provider         string  `json:"provider"` // the upstream OpenRouter routed to
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	LatencyMs        int64   `json:"latency_ms"`
}

// Driver writes one Summary.
type Driver interface {
	Summarize(ctx context.Context, pk cases.Packet) (cases.Summary, Meta, error)
}

// SystemPrompt instructs the model. It is part of the evaluated system, so
// changes to it should be re-measured (results/summaries).
const SystemPrompt = `You brief fraud analysts on a flagged card payment. The analyst must decide whether the card is compromised and what to check, and will read your brief before the raw data. Analysts are fraud specialists, not data scientists. You are given a case record as JSON; it is everything known about the case.

What makes a brief useful:
- Lead with the story, not the score. Say what happened and what pattern the facts form: is this payment part of a burst on the card, a departure from how the card is normally used, a merchant with other alerts or confirmed fraud? Use earlier_alerts, recent_behaviour, card_history, merchant_activity, related_alerts and network for this; they matter more than restating the payment.
- Make numbers comparable. "$1,631.26 in the last 24h out of $1,751.26 in 7 days" says the card woke up; "$1,631.26 in 24h" alone does not. Compare values that are in the record; do not compute new numbers (sums, differences, ratios) that are not in it.
- You may name a common fraud pattern, such as card testing (tiny charges before larger ones), cash-out through money transfers, a compromised merchant (many cards flagged at one merchant), or a refund after a large charge, only when specific facts in the record show it; say which facts, mind their order in time, and say they are consistent with the pattern, never that it is certain. Never add facts.
- Analyst outcomes (confirmed fraud, dismissed, escalated) are other analysts' judgments; cards with confirmed fraud in the network are confirmed cases. Say which you mean.
- Use merchant types, not codes (the code may follow in brackets). Describe model factors by what they mean, never by feature names, and never mention log-odds, effects or SHAP.
- A payment whose amount is negative is a refund: money returned to the card, not a purchase. Call it a refund of the amount ("a $361.00 refund"), never a payment or a charge, and do not read negative totals or ratios as spending.
- Mention the fraud probability once, as fraud_probability_percent gives it.
- Write amounts as dollars ($1,631.26). Quote every number exactly as the record gives it; do not round. Copy card, merchant and alert IDs exactly.
- Put raw units in words: a ratio of 0.0155 as 1.55% (or 12.3 as 12.3 times), seconds as the meaning beside them gives them ("39 minutes", "about 7 hours").

Do not:
- refer to "the record", "the packet" or the JSON;
- list what is absent (no errors, no location, no rule reasons) unless its absence matters; online payments never have a merchant location;
- explain one fact by another unless the record says so;
- suggest whether to confirm or dismiss the alert: the analyst decides;
- repeat a fact in two sections.

Format:
- summary: 2 or 3 sentences: what happened, the pattern, why it was flagged.
- key_points: 2 to 4 short points, each a specific fact about this payment or the card's recent activity that supports the summary.
- context: 1 to 3 short points on what the card's longer history, the merchant's other alerts, nearby alerts at the merchant and the network add. Leave a part out when it adds nothing.
- checks: 1 to 3 short, concrete next steps for the analyst, from what the record shows (for example the card's other alerts that day, whether the cardholder recognises a merchant type, or the other cards flagged at this merchant).

Reply with JSON only: {"summary": "...", "key_points": ["..."], "context": ["..."], "checks": ["..."]}`

// Stub writes a deterministic template Summary from the Packet, so the whole
// pipeline runs without an API key. It is grounded by construction.
type Stub struct{}

func (Stub) Summarize(_ context.Context, pk cases.Packet) (cases.Summary, Meta, error) {
	var why []string
	for _, f := range pk.Factors {
		if len(why) == 3 {
			break
		}
		dir := "raised"
		if f.Effect < 0 {
			dir = "lowered"
		}
		v := plain(f.Value)
		if f.Meaning != "" {
			v += ", " + f.Meaning
		}
		why = append(why, fmt.Sprintf("%s (%s) %s the risk", f.Description, v, dir))
	}
	verb := map[string]string{"block": "Blocked", "review": "Sent to review", "approve": "Approved"}[pk.Decision.Action]
	prob := cmp.Or(pk.Decision.Percent, plain(pk.Decision.Probability))
	text := fmt.Sprintf("%s with a fraud probability of %s, flagged by %s.", verb, prob, pk.Decision.FlaggedBy)
	if p := payment(pk.Payment); p != "" {
		text = p + " " + text
	}
	if len(why) > 0 {
		text += " Strongest factors: " + strings.Join(why, "; ") + "."
	}
	var points []string
	for _, r := range pk.Reasons {
		points = append(points, "Rule "+r.Code+": "+r.Description)
	}
	if pk.Anonymized != nil {
		points = append(points, "Anonymized features combined effect: "+plain(pk.Anonymized.Effect))
	}
	if len(points) > 4 {
		points = points[:4]
	}
	var context []string
	if h := pk.CardHistory; h != nil && h.Alerts > 1 {
		context = append(context, fmt.Sprintf("The %s had %d alerts in %s, %d of them blocked and %d confirmed as fraud by analysts.",
			h.Entity, h.Alerts, h.Window, h.Blocked, h.Confirmed))
	}
	if m := pk.Merchant; m != nil && m.Cards > 1 {
		context = append(context, fmt.Sprintf("The %s had %d alerts on %d cards in %s.", m.Entity, m.Alerts, m.Cards, m.Window))
	}
	if n := pk.Network; n != nil && n.FraudCards > 0 {
		context = append(context, fmt.Sprintf("%d of the %d other cards that share its merchants have confirmed fraud.", n.FraudCards, n.OtherCards))
	}
	checks := []string{"Ask the cardholder whether they recognise this payment."}
	if r := pk.Related; r != nil && r.Count > 0 {
		checks = append(checks, fmt.Sprintf("Review the %d other alerts at the %s.", r.Count, r.Merchant))
	}
	return cases.Summary{Text: text, KeyPoints: points, Context: context, Checks: checks}, Meta{Driver: "stub", Model: "template"}, nil
}

// payment says what was paid, where and how, from the Packet's facts.
func payment(facts []cases.Fact) string {
	by := map[string]cases.Fact{}
	for _, f := range facts {
		by[f.Feature] = f
	}
	amount, ok := by["amount_usd"].Value.(float64)
	if !ok {
		return ""
	}
	s := "A payment of $" + strconv.FormatFloat(amount, 'f', 2, 64)
	if amount < 0 {
		s = "A refund of $" + strconv.FormatFloat(-amount, 'f', 2, 64)
	}
	if m := by["mcc"]; m.Meaning != "" {
		s += " at " + m.Meaning + " (merchant category " + plain(m.Value) + ")"
	}
	if c := by["channel"]; c.Meaning != "" {
		s += ", " + c.Meaning
	} else if v, ok := c.Value.(string); ok {
		s += ", " + v
	}
	return s + "."
}

// plain writes numbers without exponents, as analysts read them.
func plain(v any) string {
	switch x := v.(type) {
	case nil:
		return "missing" // as the risk ledger shows it
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}
