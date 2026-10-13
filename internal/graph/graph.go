// Package graph maintains a Profile's attribute graph over a sliding window
// of Event Time and describes the Component an Event joins. Nodes (payers)
// connect through shared Link attributes (payees); attributes shared by too
// many Nodes are Super-nodes and never link.
//
// Union-find cannot delete edges, so the graph is rebuilt from the window's
// links every Rebuild interval and only grows in between. Everything is
// processed in input order, so the result is deterministic.
package graph

import (
	"fmt"
	"math"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

// Columns names the features Process returns for an Event: the Component
// (connected Nodes and Links) and the Community (its Louvain partition, which
// matters because chains of shared attributes merge thousands of Nodes into
// one Component).
var Columns = []string{"graph_nodes", "graph_links", "graph_fraud_nodes", "graph_community_nodes", "graph_community_fraud"}

// Describe explains a graph feature column for Case Packets.
func Describe(cfg profile.GraphConfig, column string) (string, bool) {
	n, l := cfg.NodeNoun, cfg.LinkNoun
	switch column {
	case "graph_nodes":
		return fmt.Sprintf("%s connected to this one through shared %s (last %s)", n, l, profile.WindowLabel(cfg.Window)), true
	case "graph_links":
		return l + " in that connected group", true
	case "graph_fraud_nodes":
		return "other " + n + " in that group with confirmed fraud", true
	case "graph_community_nodes":
		return n + " in this one's tightly linked community", true
	case "graph_community_fraud":
		return "other " + n + " in that community with confirmed fraud", true
	}
	return "", false
}

type link struct {
	timeMs     int64
	node, attr string
}

// Graph is not safe for concurrent use.
type Graph struct {
	cfg      profile.GraphConfig
	links    []link                      // in the window, oldest first
	degree   map[string]map[string]int64 // attribute -> node -> last seen
	fraud    map[string]int64            // node -> fraud Label release time
	uf       unionFind
	comm     communities
	rings    []Ring
	rebuilt  int64
	started  bool
	window   int64
	rebuild  int64
	fraudTTL int64
}

func New(cfg profile.GraphConfig) (*Graph, error) {
	if cfg.Node == "" || len(cfg.Links) == 0 || cfg.MaxDegree < 1 || cfg.Window <= 0 || cfg.Rebuild <= 0 {
		return nil, fmt.Errorf("graph: incomplete config %+v", cfg)
	}
	g := &Graph{cfg: cfg, window: cfg.Window.Milliseconds(), rebuild: cfg.Rebuild.Milliseconds(),
		fraudTTL: cfg.FraudRetention.Milliseconds(), fraud: map[string]int64{}}
	g.reset()
	return g, nil
}

func (g *Graph) reset() {
	g.degree = map[string]map[string]int64{}
	g.uf = newUnionFind()
	g.comm = communities{of: map[string]int{}}
}

// attrKey namespaces attributes so different Link types never collide.
func attrKey(typ, id string) string { return typ + "\x00" + id }

// Process applies one input record. For an Event it first returns the
// features of the Component the Event joins (from history plus the Event's
// own attributes), then adds the Event's links.
func (g *Graph) Process(env *unfraudv1.Envelope) []float64 {
	t := env.EventTimeMs
	if !g.started {
		g.started, g.rebuilt = true, t
	}
	if t-g.rebuilt >= g.rebuild {
		g.rebuildAt(t)
	}
	switch p := env.Payload.(type) {
	case *unfraudv1.Envelope_Event:
		return g.event(t, p.Event)
	case *unfraudv1.Envelope_LabelRelease:
		g.labelRelease(t, p.LabelRelease)
	}
	return nil
}

func (g *Graph) event(t int64, ev *unfraudv1.Event) []float64 {
	node, ok := ev.Entities[g.cfg.Node]
	if !ok {
		out := make([]float64, len(Columns))
		for i := range out {
			out[i] = math.NaN()
		}
		return out
	}
	var attrs []string
	for _, typ := range g.cfg.Links {
		if id, ok := ev.Entities[typ]; ok {
			attrs = append(attrs, attrKey(typ, id))
		}
	}
	out := g.features(t, node, attrs)
	for _, a := range attrs {
		g.links = append(g.links, link{timeMs: t, node: node, attr: a})
		g.addLink(t, node, a)
	}
	return out
}

// features describes the union of the Node's Component and the Components of
// its non-Super-node attributes, as the graph stands before this Event.
func (g *Graph) features(t int64, node string, attrs []string) []float64 {
	roots := map[int]bool{}
	if i, ok := g.uf.index[node]; ok {
		roots[g.uf.find(i)] = true
	}
	for _, a := range attrs {
		if g.superNode(a, node) {
			continue
		}
		if i, ok := g.uf.index[a]; ok {
			roots[g.uf.find(i)] = true
		}
	}
	var cards, links, fraud int
	for r := range roots {
		cards += g.uf.nodes[r]
		links += g.uf.attrs[r]
		fraud += g.uf.fraud[r]
	}
	_, inGraph := g.uf.index[node]
	if !inGraph {
		cards++ // the Event's own Node joins the Component
	}
	if g.isFraud(t, node) && inGraph {
		fraud-- // count other Nodes' fraud only; the Node's own is a card feature
	}
	for _, a := range attrs {
		if _, known := g.uf.index[a]; !known && !g.superNode(a, node) {
			links++
		}
	}
	commCards, commFraud := g.communityOf(t, node, attrs)
	return []float64{float64(cards), float64(links), float64(fraud), float64(commCards), float64(commFraud)}
}

// communityOf describes the Community the Event joins: its Node's, or else
// that of its first non-Super-node attribute; a new Node with neither is a
// Community of one. Fraud counts other Nodes only.
func (g *Graph) communityOf(t int64, node string, attrs []string) (cards, fraud int) {
	c, ok := g.comm.of[node]
	if !ok {
		for _, a := range attrs {
			if g.superNode(a, node) {
				continue
			}
			if c, ok = g.comm.of[a]; ok {
				break
			}
		}
	}
	if !ok {
		return 1, 0
	}
	cards, fraud = g.comm.cards[c], g.comm.fraud[c]
	if _, member := g.comm.of[node]; !member {
		cards++
	} else if g.isFraud(t, node) {
		fraud--
	}
	return cards, fraud
}

// superNode reports whether attr would exceed MaxDegree once node uses it.
func (g *Graph) superNode(attr, node string) bool {
	d := g.degree[attr]
	n := len(d)
	if _, ok := d[node]; !ok {
		n++
	}
	return n > g.cfg.MaxDegree
}

func (g *Graph) addLink(t int64, node, attr string) {
	d := g.degree[attr]
	if d == nil {
		d = map[string]int64{}
		g.degree[attr] = d
	}
	d[node] = t
	if len(d) > g.cfg.MaxDegree {
		return // a Super-node: links already made through it stay until the next rebuild
	}
	ni := g.uf.add(node, false, g.isFraud(t, node))
	ai := g.uf.add(attr, true, false)
	g.uf.union(ni, ai)
	g.comm.link(node, attr, g.isFraud(t, node))
}

func (g *Graph) isFraud(t int64, node string) bool {
	at, ok := g.fraud[node]
	return ok && t-at <= g.fraudTTL
}

func (g *Graph) labelRelease(t int64, lr *unfraudv1.LabelRelease) {
	if lr.Label != unfraudv1.Label_LABEL_FRAUD {
		return
	}
	node, ok := lr.Entities[g.cfg.Node]
	if !ok {
		return
	}
	was := g.isFraud(t, node)
	g.fraud[node] = t
	if i, ok := g.uf.index[node]; ok && !was {
		g.uf.fraud[g.uf.find(i)]++
	}
	if c, ok := g.comm.of[node]; ok && !was {
		g.comm.fraud[c]++
	}
}

// Ring is a Community judged suspicious at a rebuild.
type Ring struct {
	ID         string // "<rebuild time>-<community>", unique per rebuild
	DetectedMs int64
	Nodes      []string
	Fraud      int // Nodes with released fraud Labels
}

// Rings returns the Rings found at the most recent rebuild, once: later calls
// return nil until the next rebuild.
func (g *Graph) Rings() []Ring {
	r := g.rings
	g.rings = nil
	return r
}

func (g *Graph) detectRings(t int64) {
	g.rings = nil
	if g.cfg.RingMinNodes == 0 {
		return
	}
	for c, n := range g.comm.cards {
		f := g.comm.fraud[c]
		if n >= g.cfg.RingMinNodes && f >= g.cfg.RingMinFraud && float64(f) >= g.cfg.RingMinShare*float64(n) {
			g.rings = append(g.rings, Ring{
				ID: fmt.Sprintf("%d-%d", t, c), DetectedMs: t,
				Nodes: append([]string(nil), g.comm.members[c]...), Fraud: f,
			})
		}
	}
}

// rebuildAt drops links older than the window and rebuilds the union-find
// from the rest, recomputing Super-nodes and fraud counts.
func (g *Graph) rebuildAt(t int64) {
	g.rebuilt = t
	drop := 0
	for drop < len(g.links) && g.links[drop].timeMs < t-g.window {
		drop++
	}
	g.links = append([]link(nil), g.links[drop:]...)
	for n, at := range g.fraud {
		if t-at > g.fraudTTL {
			delete(g.fraud, n)
		}
	}
	g.reset()
	for _, l := range g.links {
		d := g.degree[l.attr]
		if d == nil {
			d = map[string]int64{}
			g.degree[l.attr] = d
		}
		d[l.node] = l.timeMs
	}
	var adj [][]wedge
	seen := map[[2]int]bool{}
	for _, l := range g.links {
		if len(g.degree[l.attr]) > g.cfg.MaxDegree {
			continue
		}
		ni, ai := g.uf.add(l.node, false, g.isFraud(t, l.node)), g.uf.add(l.attr, true, false)
		g.uf.union(ni, ai)
		for len(adj) < len(g.uf.parent) {
			adj = append(adj, nil)
		}
		if !seen[[2]int{ni, ai}] {
			seen[[2]int{ni, ai}] = true
			adj[ni] = append(adj[ni], wedge{ai, 1})
			adj[ai] = append(adj[ai], wedge{ni, 1})
		}
	}
	part := louvain(adj)
	keys := make([]string, len(g.uf.parent))
	for k, i := range g.uf.index {
		keys[i] = k
	}
	for i, c := range part {
		for len(g.comm.cards) <= c {
			g.comm.cards = append(g.comm.cards, 0)
			g.comm.fraud = append(g.comm.fraud, 0)
			g.comm.members = append(g.comm.members, nil)
		}
		g.comm.of[keys[i]] = c
		if !g.uf.isAttr[i] {
			g.comm.cards[c]++
			g.comm.members[c] = append(g.comm.members[c], keys[i])
			if g.isFraud(t, keys[i]) {
				g.comm.fraud[c]++
			}
		}
	}
	g.detectRings(t)
}

// communities is the latest Louvain partition, extended between rebuilds:
// a new Node or attribute joins its linked partner's Community.
type communities struct {
	of      map[string]int
	cards   []int
	fraud   []int
	members [][]string // Nodes per Community, in order of joining
}

func (c *communities) link(node, attr string, fraud bool) {
	cn, nok := c.of[node]
	ca, aok := c.of[attr]
	switch {
	case nok && aok:
	case aok:
		c.of[node] = ca
		c.cards[ca]++
		c.members[ca] = append(c.members[ca], node)
		if fraud {
			c.fraud[ca]++
		}
	case nok:
		c.of[attr] = cn
	default:
		id := len(c.cards)
		c.of[node], c.of[attr] = id, id
		c.cards = append(c.cards, 1)
		c.fraud = append(c.fraud, 0)
		c.members = append(c.members, []string{node})
		if fraud {
			c.fraud[id] = 1
		}
	}
}

// Stats summarises the graph, for diagnostics.
type Stats struct {
	Links, Nodes, Components, Largest int
}

func (g *Graph) Stats() Stats {
	s := Stats{Links: len(g.links)}
	for r := range g.uf.parent {
		if g.uf.parent[r] != r {
			continue
		}
		s.Components++
		s.Nodes += g.uf.nodes[r]
		s.Largest = max(s.Largest, g.uf.nodes[r])
	}
	return s
}

// unionFind keeps, per root, how many Nodes, attributes and fraud Nodes its
// Component has.
type unionFind struct {
	index  map[string]int
	isAttr []bool
	parent []int
	rank   []int
	nodes  []int
	attrs  []int
	fraud  []int
}

func newUnionFind() unionFind { return unionFind{index: map[string]int{}} }

func (u *unionFind) add(key string, attr, fraud bool) int {
	if i, ok := u.index[key]; ok {
		return i
	}
	i := len(u.parent)
	u.index[key] = i
	u.parent = append(u.parent, i)
	u.isAttr = append(u.isAttr, attr)
	u.rank = append(u.rank, 0)
	n, a, f := 1, 0, 0
	if attr {
		n, a = 0, 1
	}
	if fraud {
		f = 1
	}
	u.nodes = append(u.nodes, n)
	u.attrs = append(u.attrs, a)
	u.fraud = append(u.fraud, f)
	return i
}

func (u *unionFind) find(i int) int {
	for u.parent[i] != i {
		u.parent[i] = u.parent[u.parent[i]]
		i = u.parent[i]
	}
	return i
}

func (u *unionFind) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if u.rank[ra] < u.rank[rb] {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
	if u.rank[ra] == u.rank[rb] {
		u.rank[ra]++
	}
	u.nodes[ra] += u.nodes[rb]
	u.attrs[ra] += u.attrs[rb]
	u.fraud[ra] += u.fraud[rb]
}
