// Package cases builds Case Packets, the only facts an LLM Summary may use,
// and checks Summaries against them (Grounding).
package cases

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/graph"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
	"github.com/kanishka-sahoo/unfraud/internal/scoring"
	"github.com/kanishka-sahoo/unfraud/internal/store"
)

// Packet is serialised to JSON and sent to the LLM as-is.
type Packet struct {
	CaseID    string         `json:"case_id"`
	EventTime string         `json:"event_time"`
	Decision  PacketDecision `json:"decision"`
	// Payment describes the payment itself: amount, merchant type, channel.
	Payment []Fact `json:"payment"`
	// Behaviour describes the card's recent activity, whether or not the
	// model weighed it heavily.
	Behaviour []Fact `json:"recent_behaviour,omitempty"`
	// Earlier lists the card's alerts in the days before this one.
	Earlier  *Earlier          `json:"earlier_alerts,omitempty"`
	Reasons  []PacketReason    `json:"rule_reasons,omitempty"`
	Entities map[string]string `json:"entities"`
	Factors  []Factor          `json:"model_factors"`
	// Anonymized combines every masked feature's effect: the dataset does
	// not say what they mean, so they are never described individually.
	Anonymized *Factor `json:"anonymized_features,omitempty"`
	// The rest is what an analyst would look up next (internal/investigate):
	// the card's longer history, the merchant, the network around the card,
	// other cards' alerts at the merchant, and both Stages' Decisions.
	CardHistory *History          `json:"card_history,omitempty"`
	Merchant    *MerchantActivity `json:"merchant_activity,omitempty"`
	Network     *Network          `json:"network,omitempty"`
	Related     *Related          `json:"related_alerts,omitempty"`
	Stages      []Stage           `json:"decisions_by_stage,omitempty"`
	Notes       []string          `json:"notes"`
}

type PacketDecision struct {
	Action      string  `json:"action"`
	Probability float64 `json:"fraud_probability"`
	Percent     string  `json:"fraud_probability_percent"` // as analysts read it
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
	Meaning     string  `json:"meaning,omitempty"` // what a coded value stands for
	Effect      float64 `json:"effect"`
}

// Earlier summarizes an Entity's Cases in the window before this one, so a
// Summary can say this is the seventh alert on the card today. Totals are
// precomputed: Grounding only accepts numbers found in the Packet.
type Earlier struct {
	Entity         string         `json:"entity"` // "card 77-1"
	Window         string         `json:"window"` // "72h"
	Count          int            `json:"count"`
	Blocked        int            `json:"blocked"`
	TotalAmountUSD float64        `json:"total_amount_usd"`
	Alerts         []EarlierAlert `json:"alerts"`
}

type EarlierAlert struct {
	AlertID   string `json:"alert_id"`
	EventTime string `json:"event_time"`
	Decision  string `json:"decision"`
	AmountUSD any    `json:"amount_usd"`
	Merchant  string `json:"merchant_type,omitempty"`
	MCC       any    `json:"mcc,omitempty"`
	Channel   any    `json:"channel,omitempty"`
	Percent   string `json:"fraud_probability_percent"`
}

// EarlierWindow is how far back Earlier looks.
const EarlierWindow = 72 * time.Hour

// Fact is one described feature value of a Case's Event.
type Fact struct {
	Feature     string `json:"feature"`
	Description string `json:"description"`
	Value       any    `json:"value"` // number, category, or null when missing
	Meaning     string `json:"meaning,omitempty"`
}

// Facts describes a Case's feature values: the headline features first, in
// the Profile's order, then every other described feature by name. Cases
// explained before all values were stored fall back to the top
// Contributions' values.
func Facts(p profile.Profile, c *store.CaseContributions) []Fact {
	if c == nil {
		return nil
	}
	values := c.Values
	if values == nil {
		values = map[string]any{}
		for _, t := range c.Top {
			values[t.Feature] = t.Value
		}
	}
	var out []Fact
	add := func(f string) {
		v, ok := values[f]
		if !ok {
			return
		}
		desc, ok := describe(p, f)
		if !ok {
			return // anonymized
		}
		out = append(out, Fact{Feature: f, Description: desc, Value: roundAny(v), Meaning: p.Meaning(f, v)})
	}
	for _, f := range p.Headline {
		add(f)
	}
	rest := make([]string, 0, len(values))
	for f := range values {
		if !slices.Contains(p.Headline, f) {
			rest = append(rest, f)
		}
	}
	slices.Sort(rest)
	for _, f := range rest {
		add(f)
	}
	return out
}

// Build makes a Packet from a Case and the earlier Cases on its Subject
// (store.PriorCases over EarlierWindow). It needs the Case's Contributions.
func Build(p profile.Profile, c store.Case, earlier []store.CaseListItem) (Packet, error) {
	if c.Contributions == nil {
		return Packet{}, fmt.Errorf("case %s/%s has no contributions yet", c.Profile, c.EventID)
	}
	pk := Packet{
		CaseID:    c.EventID,
		EventTime: c.EventTime.UTC().Format(time.RFC3339),
		Decision: PacketDecision{
			Action:      profile.Action(c.Action).String(),
			Probability: round(float64(c.Probability), 4),
			Percent:     percent(float64(c.Probability)),
			FlaggedBy:   map[int16]string{1: "the real-time scorer on arrival", 2: "the graph revision"}[c.FlaggedBy],
		},
		Entities: c.Entities,
		Notes: []string{
			"payment facts with a meaning field are coded values; the meaning says what the code stands for",
			"effects are in log-odds: positive values raise the fraud probability, negative values lower it",
			"event times are reconstructed from the public dataset and are not real calendar dates",
			"earlier_alerts are this card's alerts in the window before this payment, oldest first",
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
			Meaning: p.Meaning(f.Feature, f.Value), Effect: round(f.SHAP, 3)})
	}
	for _, f := range Facts(p, c.Contributions) {
		if slices.Contains(p.Headline, f.Feature) {
			pk.Payment = append(pk.Payment, f)
		}
		if slices.Contains(p.Behaviour, f.Feature) {
			pk.Behaviour = append(pk.Behaviour, f)
		}
	}
	if len(earlier) > 0 {
		e := &Earlier{Entity: p.Noun(p.Subject) + " " + c.Entities[p.Subject], Window: profile.WindowLabel(EarlierWindow),
			Count: len(earlier)}
		for _, x := range earlier {
			a := EarlierAlert{AlertID: x.EventID, EventTime: x.EventTime.UTC().Format("2006-01-02 15:04"), Decision: profile.Action(x.Action).String(),
				AmountUSD: roundAny(x.Values["amount_usd"]), MCC: x.Values["mcc"], Channel: x.Values["channel"],
				Merchant: p.Meaning("mcc", x.Values["mcc"]), Percent: percent(float64(x.Probability))}
			if x.Action == int16(profile.Block) {
				e.Blocked++
			}
			if v, ok := x.Values["amount_usd"].(float64); ok {
				e.TotalAmountUSD += v
			}
			e.Alerts = append(e.Alerts, a)
		}
		e.TotalAmountUSD = round(e.TotalAmountUSD, 2)
		pk.Earlier = e
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

// Percent writes a probability as the console does.
func Percent(p float64) string { return percent(p) }

// RoundAny keeps numbers short so an LLM can quote them exactly.
func RoundAny(v any) any { return roundAny(v) }

// percent writes a probability as the console does, never rounding up to
// certainty or down to impossibility.
func percent(p float64) string {
	switch {
	case p >= 0.9995:
		return ">99.9%"
	case p > 0 && p < 0.0005:
		return "<0.1%"
	}
	return strconv.FormatFloat(p*100, 'f', 1, 64) + "%"
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
