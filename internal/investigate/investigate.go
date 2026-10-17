// Package investigate gathers what an analyst would look up after reading
// an alert: the card's longer history, the merchant's other alerts, the
// network around the card, other cards' alerts at the merchant, and both
// Stages' Decisions (ADR-0022). Summaries get it all in their Case Packet;
// the assistant's tools look each part up on demand.
package investigate

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/kanishka-sahoo/unfraud/internal/cases"
	"github.com/kanishka-sahoo/unfraud/internal/graph"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
	"github.com/kanishka-sahoo/unfraud/internal/store"
)

// Windows the Case Packet uses.
const (
	HistoryDays  = 30
	MerchantDays = 7
	RelatedHours = 24
	NetworkHops  = 2
)

// Graph returns a node's neighbourhood from graph-svc.
type Graph interface {
	Neighborhood(ctx context.Context, profile, node string, hops int) (graph.Subgraph, error)
}

// HTTPGraph calls graph-svc's /neighborhood.
type HTTPGraph struct {
	URL func(profile string) string
}

func (g HTTPGraph) Neighborhood(ctx context.Context, profile, node string, hops int) (graph.Subgraph, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u := g.URL(profile) + "/neighborhood?node=" + url.QueryEscape(node) + "&hops=" + strconv.Itoa(hops)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return graph.Subgraph{}, fmt.Errorf("graph-svc unavailable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return graph.Subgraph{}, fmt.Errorf("graph-svc: %s", res.Status)
	}
	var sg graph.Subgraph
	return sg, json.NewDecoder(res.Body).Decode(&sg)
}

// Lookup reads Cases from the store and networks from graph-svc.
type Lookup struct {
	Store *store.Store
	Graph Graph // nil leaves networks out
}

// Card and Merchant are a Profile's Entity types for the card (the Subject)
// and the merchant it paid.
func Card(p profile.Profile) string { return p.Subject }

func Merchant(p profile.Profile) string {
	keys := make([]string, 0, len(p.Nouns))
	for k := range p.Nouns {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if p.Nouns[k] == "merchant" {
			return k
		}
	}
	if p.Graph != nil && len(p.Graph.Links) > 0 {
		return p.Graph.Links[0]
	}
	return ""
}

// Packet builds a Case's full Case Packet. Parts that can't be looked up
// (graph-svc down, say) are left out and noted.
func (l Lookup) Packet(ctx context.Context, c store.Case) (cases.Packet, error) {
	p, err := registry.Get(c.Profile)
	if err != nil {
		return cases.Packet{}, err
	}
	card, merchant := c.Entities[Card(p)], c.Entities[Merchant(p)]
	var earlier []store.CaseListItem
	if card != "" {
		if earlier, err = l.Store.PriorCases(ctx, c.Profile, Card(p), card, c.EventTime, cases.EarlierWindow, 12); err != nil {
			return cases.Packet{}, err
		}
	}
	pk, err := cases.Build(p, c, earlier)
	if err != nil {
		return pk, err
	}
	if card != "" {
		if pk.CardHistory, _, err = l.CardHistory(ctx, p, card, c.EventTime, HistoryDays); err != nil {
			return pk, err
		}
	}
	if merchant != "" {
		if pk.Merchant, _, err = l.MerchantActivity(ctx, p, merchant, c.EventTime, MerchantDays); err != nil {
			return pk, err
		}
		if pk.Related, err = l.Related(ctx, p, c.EventID, merchant, card, c.EventTime, RelatedHours); err != nil {
			return pk, err
		}
	}
	if card != "" && l.Graph != nil {
		if n, _, err := l.Network(ctx, p, card, NetworkHops); err == nil {
			pk.Network = n
			pk.Notes = append(pk.Notes, "network is the card's neighbourhood in the graph as of graph_as_of, not at the payment's time")
		} else {
			pk.Notes = append(pk.Notes, "the network around the card could not be looked up")
		}
	}
	ds, err := l.Store.EventDecisions(ctx, store.CaseKey{Profile: c.Profile, EventID: c.EventID})
	if err != nil {
		return pk, err
	}
	pk.Stages = Stages(ds)
	pk.Notes = append(pk.Notes,
		"card_history and merchant_activity count alerts (flagged payments) only, up to and including this payment; analyst outcomes are judgments, not confirmed labels",
		"related_alerts are other cards' alerts at the same merchant near this payment")
	return pk, nil
}

// CardHistory summarizes a card's alerts in the days up to and including at.
func (l Lookup) CardHistory(ctx context.Context, p profile.Profile, card string, at time.Time, days int) (*cases.History, []store.CaseListItem, error) {
	cs, total, err := l.Store.FindCases(ctx, store.CaseSearch{Profile: p.Name, Entities: map[string]string{Card(p): card},
		From: at.Add(-time.Duration(days) * 24 * time.Hour), To: at.Add(time.Second)})
	if err != nil {
		return nil, nil, err
	}
	h := &cases.History{Entity: p.Noun(Card(p)) + " " + card, Window: Days(days), Alerts: total}
	types := map[string]*cases.TypeCount{}
	for _, c := range cs {
		count(c, &h.Blocked, &h.Confirmed, &h.Dismissed, &h.Escalated, &h.Open)
		amount, _ := c.Values["amount_usd"].(float64)
		h.TotalAmountUSD += amount
		mt := cmp.Or(p.Meaning("mcc", c.Values["mcc"]), "unknown")
		if types[mt] == nil {
			types[mt] = &cases.TypeCount{MerchantType: mt, MCC: c.Values["mcc"]}
		}
		types[mt].Alerts++
		types[mt].AmountUSD += amount
	}
	h.TotalAmountUSD = round2(h.TotalAmountUSD)
	if len(cs) > 0 {
		h.LastAlert, h.FirstAlert = When(cs[0].EventTime), When(cs[len(cs)-1].EventTime)
	}
	if len(cs) < total {
		h.Shown = fmt.Sprintf("the newest %d of %d alerts", len(cs), total)
	}
	for _, t := range types {
		t.AmountUSD = round2(t.AmountUSD)
		h.ByMerchantType = append(h.ByMerchantType, *t)
	}
	slices.SortFunc(h.ByMerchantType, func(a, b cases.TypeCount) int {
		return cmp.Or(b.Alerts-a.Alerts, cmp.Compare(a.MerchantType, b.MerchantType))
	})
	if len(h.ByMerchantType) > 5 {
		h.ByMerchantType = h.ByMerchantType[:5]
	}
	return h, cs, nil
}

// MerchantActivity summarizes a merchant's alerts on any card in the days
// up to and including at.
func (l Lookup) MerchantActivity(ctx context.Context, p profile.Profile, merchant string, at time.Time, days int) (*cases.MerchantActivity, []store.CaseListItem, error) {
	cs, total, err := l.Store.FindCases(ctx, store.CaseSearch{Profile: p.Name, Entities: map[string]string{Merchant(p): merchant},
		From: at.Add(-time.Duration(days) * 24 * time.Hour), To: at.Add(time.Second)})
	if err != nil {
		return nil, nil, err
	}
	m := &cases.MerchantActivity{Entity: p.Noun(Merchant(p)) + " " + merchant, Window: Days(days), Alerts: total}
	cards := map[string]bool{}
	var escalated, open int
	for _, c := range cs {
		count(c, &m.Blocked, &m.Confirmed, &m.Dismissed, &escalated, &open)
		cards[c.Entities[Card(p)]] = true
		if m.MerchantType == "" {
			m.MerchantType = p.Meaning("mcc", c.Values["mcc"])
		}
	}
	m.Cards = len(cards)
	if len(cs) < total {
		m.Shown = fmt.Sprintf("the newest %d of %d alerts", len(cs), total)
	}
	return m, cs, nil
}

// Related lists other cards' alerts at a merchant within hours of at,
// nearest first.
func (l Lookup) Related(ctx context.Context, p profile.Profile, caseID, merchant, card string, at time.Time, hours int) (*cases.Related, error) {
	w := time.Duration(hours) * time.Hour
	cs, _, err := l.Store.FindCases(ctx, store.CaseSearch{Profile: p.Name, Entities: map[string]string{Merchant(p): merchant},
		From: at.Add(-w), To: at.Add(w)})
	if err != nil {
		return nil, err
	}
	r := &cases.Related{Merchant: p.Noun(Merchant(p)) + " " + merchant, Window: fmt.Sprintf("%d hours either side", hours)}
	cards := map[string]bool{}
	var others []store.CaseListItem
	for _, c := range cs {
		if c.EventID == caseID || c.Entities[Card(p)] == card {
			continue
		}
		others = append(others, c)
		cards[c.Entities[Card(p)]] = true
	}
	r.Count, r.Cards = len(others), len(cards)
	slices.SortStableFunc(others, func(a, b store.CaseListItem) int {
		return cmp.Compare(abs(a.EventTime.Sub(at)), abs(b.EventTime.Sub(at)))
	})
	for _, c := range others[:min(8, len(others))] {
		r.Alerts = append(r.Alerts, cases.RelatedAlert{AlertID: c.EventID, Card: c.Entities[Card(p)], EventTime: When(c.EventTime),
			Decision: profile.Action(c.Action).String(), AmountUSD: cases.RoundAny(c.Values["amount_usd"]),
			Percent: cases.Percent(float64(c.Probability)), Disposition: deref(c.Disposition)})
	}
	return r, nil
}

// Network summarizes a card's neighbourhood in graph-svc's graph, and how
// each card with confirmed fraud in it connects to the card.
func (l Lookup) Network(ctx context.Context, p profile.Profile, card string, hops int) (*cases.Network, graph.Subgraph, error) {
	if l.Graph == nil {
		return nil, graph.Subgraph{}, fmt.Errorf("no graph")
	}
	sg, err := l.Graph.Neighborhood(ctx, p.Name, card, hops)
	if err != nil {
		return nil, sg, err
	}
	n := &cases.Network{Entity: p.Noun(Card(p)) + " " + card, Hops: hops, Truncated: sg.Truncated}
	adj := map[string][]string{}
	for _, e := range sg.Edges {
		adj[e.Source] = append(adj[e.Source], e.Target)
		adj[e.Target] = append(adj[e.Target], e.Source)
	}
	own := map[string]bool{} // the card's own merchants
	for _, m := range adj[sg.Center] {
		own[m] = true
	}
	linked := map[string]int{} // own merchant -> other cards linked through it
	var fraud []string
	for _, x := range sg.Nodes {
		switch {
		case x.ID == sg.Center:
			n.CardHasFraud = x.Fraud
		case x.Kind == Card(p):
			n.OtherCards++
			for _, m := range adj[x.ID] {
				if own[m] {
					linked[m]++
				}
			}
			if x.Fraud {
				n.FraudCards++
				fraud = append(fraud, x.ID)
			}
		default:
			n.Merchants++
			if x.SuperNode {
				n.BusyMerchants++
			}
		}
	}
	for _, c := range fraud[:min(10, len(fraud))] {
		fl := cases.FraudLink{Card: c}
		for _, m := range adj[c] {
			if own[m] {
				fl.SharedMerchants = append(fl.SharedMerchants, m)
			}
		}
		slices.Sort(fl.SharedMerchants)
		if len(fl.SharedMerchants) == 0 {
			fl.Via = "no shared merchant: it used a merchant that another linked card also used"
		}
		n.FraudLinks = append(n.FraudLinks, fl)
	}
	for m, k := range linked {
		n.TopMerchants = append(n.TopMerchants, cases.MerchantLink{Merchant: m, Cards: k})
	}
	slices.SortFunc(n.TopMerchants, func(a, b cases.MerchantLink) int {
		return cmp.Or(b.Cards-a.Cards, cmp.Compare(a.Merchant, b.Merchant))
	})
	n.TopMerchants = n.TopMerchants[:min(5, len(n.TopMerchants))]
	if sg.AsOf > 0 {
		n.AsOf = When(time.UnixMilli(sg.AsOf))
	}
	n.Note = "this is the graph as it is now (graph_as_of), not at the payment's time, and it changes as payments stream in; " +
		"merchants are the ones the card used in the 30 days before graph_as_of, so a payment's merchant can have aged out; " +
		"other cards are ones that used the same merchants"
	if hops <= 2 {
		n.Note += "; at this depth every other card shares at least one merchant with the card"
	}
	if n.BusyMerchants > 0 {
		n.Note += "; merchants used by very many cards are not linked through, so their cards are not counted"
	}
	if n.Truncated {
		n.Note += "; the graph service returns at most 300 cards and merchants, so the counts are a lower bound"
	}
	return n, sg, nil
}

// Stages describes each Stage's Decision.
func Stages(ds []store.Decision) []cases.Stage {
	var out []cases.Stage
	for _, d := range ds {
		out = append(out, cases.Stage{Stage: map[int16]string{1: "real-time scorer", 2: "graph revision"}[d.Stage],
			Decision: profile.Action(d.Action).String(), Percent: cases.Percent(float64(d.Probability)), Model: d.ModelVersion})
	}
	return out
}

func count(c store.CaseListItem, blocked, confirmed, dismissed, escalated, open *int) {
	if c.Action == int16(profile.Block) {
		*blocked++
	}
	switch deref(c.Disposition) {
	case "confirmed":
		*confirmed++
	case "dismissed":
		*dismissed++
	case "escalated":
		*escalated++
	default:
		*open++
	}
}

// When writes a dataset time as the console does.
func When(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") }

// Days writes a window in days.
func Days(n int) string {
	if n == 1 {
		return "1 day"
	}
	return strconv.Itoa(n) + " days"
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func abs(d time.Duration) time.Duration { return max(d, -d) }

func round2(v float64) float64 {
	r, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 2, 64), 64)
	return r
}
