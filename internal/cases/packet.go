// Package cases builds Case Packets, the only facts an LLM Summary may use,
// and checks Summaries against them (Grounding).
package cases

import (
	"fmt"
	"math"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/graph"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
	"github.com/kanishka-sahoo/unfraud/internal/scoring"
	"github.com/kanishka-sahoo/unfraud/internal/store"
)

// Packet is serialised to JSON and sent to the LLM as-is.
type Packet struct {
	CaseID    string            `json:"case_id"`
	Dataset   string            `json:"dataset"`
	EventTime string            `json:"event_time"`
	Decision  PacketDecision    `json:"decision"`
	Reasons   []PacketReason    `json:"rule_reasons,omitempty"`
	Entities  map[string]string `json:"entities"`
	Factors   []Factor          `json:"model_factors"`
	// Anonymized combines every masked feature's effect: the dataset does
	// not say what they mean, so they are never described individually.
	Anonymized *Factor  `json:"anonymized_features,omitempty"`
	Notes      []string `json:"notes"`
}

type PacketDecision struct {
	Action      string  `json:"action"`
	Probability float64 `json:"fraud_probability"`
	FlaggedBy   string  `json:"flagged_by"`
}

type PacketReason struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

// Factor is one feature's effect on the score, in log-odds: positive raises
// the fraud probability.
type Factor struct {
	Feature     string  `json:"feature"`
	Description string  `json:"description"`
	Value       any     `json:"value,omitempty"`
	Effect      float64 `json:"effect"`
}

// Build makes a Packet from a Case. It needs the Case's Contributions.
func Build(p profile.Profile, c store.Case) (Packet, error) {
	if c.Contributions == nil {
		return Packet{}, fmt.Errorf("case %s/%s has no contributions yet", c.Profile, c.EventID)
	}
	pk := Packet{
		CaseID:    c.Profile + "/" + c.EventID,
		Dataset:   c.Profile,
		EventTime: c.EventTime.UTC().Format(time.RFC3339),
		Decision: PacketDecision{
			Action:      profile.Action(c.Action).String(),
			Probability: round(float64(c.Probability), 4),
			FlaggedBy:   map[int16]string{1: "the real-time scorer on arrival", 2: "the graph revision"}[c.FlaggedBy],
		},
		Entities: c.Entities,
		Notes: []string{
			"effects are in log-odds: positive values raise the fraud probability, negative values lower it",
			"event times are reconstructed from the public dataset and are not real calendar dates",
		},
	}
	rules := map[string]string{scoring.RingMember: "the card belongs to a detected fraud ring"}
	for _, r := range p.Rules {
		rules[r.Code] = r.Description
	}
	for _, code := range c.ReasonCodes {
		pk.Reasons = append(pk.Reasons, PacketReason{Code: code, Description: rules[code]})
	}
	var anon float64
	var nAnon int
	for _, f := range c.Contributions.Top {
		desc, ok := describe(p, f.Feature)
		if !ok {
			anon += f.SHAP
			nAnon++
			continue
		}
		pk.Factors = append(pk.Factors, Factor{Feature: f.Feature, Description: desc, Value: roundAny(f.Value),
			Effect: round(f.SHAP, 3)})
	}
	if nAnon > 0 {
		pk.Anonymized = &Factor{Feature: "anonymized",
			Description: fmt.Sprintf("combined effect of %d masked features among the strongest factors", nAnon),
			Effect:      round(anon, 3)}
	}
	return pk, nil
}

// describe explains a feature, or reports it as anonymized.
func describe(p profile.Profile, feature string) (string, bool) {
	if d, ok := p.Describe(feature); ok {
		return d, true
	}
	if p.Graph != nil {
		if d, ok := graph.Describe(*p.Graph, feature); ok {
			return d, true
		}
	}
	if d, ok := p.Glossary[feature]; ok {
		return d, true
	}
	return "", false
}

// round keeps numbers short so the LLM can quote them exactly.
func round(v float64, decimals int) float64 {
	m := math.Pow(10, float64(decimals))
	return math.Round(v*m) / m
}

func roundAny(v any) any {
	f, ok := v.(float64)
	if !ok {
		return v
	}
	if math.Abs(f) >= 100 || f == math.Trunc(f) {
		return round(f, 2)
	}
	return round(f, 4)
}
