// Package llm writes Case Summaries through a pluggable driver: a stub that
// needs no API key, or OpenRouter (ADR-0009).
package llm

import (
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
const SystemPrompt = `You write short summaries of flagged payment cases for fraud analysts.

Rules:
- Use only facts in the case packet the user sends. Never add facts, guesses or outside knowledge.
- Quote numbers exactly as they appear in the packet (you may round or show a probability as a percentage).
- Do not speculate about anonymized features; mention their combined effect only if it matters.
- Explain why the case was flagged: the decision, the strongest model factors and any rule reasons.
- Plain language, no jargon such as "log-odds" or "SHAP"; say "raised the risk" or "lowered the risk".
- summary: 2 to 4 sentences. key_points: up to 4 short bullet facts.

Reply with JSON only: {"summary": "...", "key_points": ["..."]}`

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
		why = append(why, fmt.Sprintf("%s (%s) %s the risk", f.Feature, plain(f.Value), dir))
	}
	text := fmt.Sprintf("Case %s was sent to %s with a fraud probability of %s, flagged by %s.",
		pk.CaseID, pk.Decision.Action, plain(pk.Decision.Probability), pk.Decision.FlaggedBy)
	if len(why) > 0 {
		text += " Strongest named factors: " + strings.Join(why, "; ") + "."
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
	return cases.Summary{Text: text, KeyPoints: points}, Meta{Driver: "stub", Model: "template"}, nil
}

// plain writes numbers without exponents, as analysts read them.
func plain(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}
