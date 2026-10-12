package graph

import (
	"sort"
	"strings"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
)

// Subgraph is a Node's neighbourhood, for the web app's graph explorer.
type Subgraph struct {
	Center    string  `json:"center"`
	Nodes     []GNode `json:"nodes"`
	Edges     []GEdge `json:"edges"`
	Truncated bool    `json:"truncated"`
}

type GNode struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`  // card, device, tx
	Fraud     bool   `json:"fraud"` // a released fraud Label
	SuperNode bool   `json:"super_node,omitempty"`
	Community *int   `json:"community,omitempty"`
}

type GEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// bfs walks adj from center, up to hops away and maxNodes in total.
func bfs(center string, hops, maxNodes int, adj func(string) []string) ([]string, [][2]string, bool) {
	seen := map[string]bool{center: true}
	order := []string{center}
	var edges [][2]string
	frontier := []string{center}
	truncated := false
	for h := 0; h < hops && len(frontier) > 0; h++ {
		var next []string
		for _, n := range frontier {
			nbrs := adj(n)
			sort.Strings(nbrs)
			for _, m := range nbrs {
				if !seen[m] {
					if len(order) >= maxNodes {
						truncated = true
						continue
					}
					seen[m] = true
					order = append(order, m)
					next = append(next, m)
				}
				if seen[m] {
					edges = append(edges, [2]string{n, m})
				}
			}
		}
		frontier = next
	}
	return order, dedupEdges(edges), truncated
}

func dedupEdges(es [][2]string) [][2]string {
	seen := map[[2]string]bool{}
	var out [][2]string
	for _, e := range es {
		k := e
		if k[1] < k[0] {
			k[0], k[1] = k[1], k[0]
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	return out
}

// Neighborhood returns a card's neighbourhood in the attribute graph over
// the current window: cards and the devices they share.
func (g *Graph) Neighborhood(t int64, node string, hops, maxNodes int) Subgraph {
	adj := map[string]map[string]bool{}
	add := func(a, b string) {
		if adj[a] == nil {
			adj[a] = map[string]bool{}
		}
		adj[a][b] = true
	}
	for _, l := range g.links {
		add(l.node, l.attr)
		add(l.attr, l.node)
	}
	nodes, edges, truncated := bfs(node, hops, maxNodes, func(n string) []string {
		var out []string
		for m := range adj[n] {
			out = append(out, m)
		}
		return out
	})
	sg := Subgraph{Center: node, Truncated: truncated}
	for _, n := range nodes {
		gn := GNode{ID: n, Kind: "card"}
		if typ, id, ok := strings.Cut(n, "\x00"); ok {
			gn.ID, gn.Kind = id, typ
			gn.SuperNode = len(g.degree[n]) > g.cfg.MaxDegree
		} else {
			gn.Fraud = g.isFraud(t, n)
		}
		if c, ok := g.comm.of[n]; ok {
			gn.Community = &c
		}
		sg.Nodes = append(sg.Nodes, gn)
	}
	for _, e := range edges {
		sg.Edges = append(sg.Edges, GEdge{Source: display(e[0]), Target: display(e[1])})
	}
	return sg
}

func display(key string) string {
	if _, id, ok := strings.Cut(key, "\x00"); ok {
		return id
	}
	return key
}

// Neighborhood returns a transaction's payment neighbourhood over all closed
// periods.
func (f *Flow) Neighborhood(node string, hops, maxNodes int) Subgraph {
	nodes, edges, truncated := bfs(node, hops, maxNodes, func(n string) []string { return f.history[n] })
	sg := Subgraph{Center: node, Truncated: truncated}
	for _, n := range nodes {
		sg.Nodes = append(sg.Nodes, GNode{ID: n, Kind: "tx", Fraud: f.fraud[n]})
	}
	for _, e := range edges {
		sg.Edges = append(sg.Edges, GEdge{Source: e[0], Target: e[1]})
	}
	return sg
}

// observe keeps what the explorer needs from records Flow otherwise ignores.
func (f *Flow) observe(env *unfraudv1.Envelope) {
	if lr := env.GetLabelRelease(); lr != nil && lr.Label == unfraudv1.Label_LABEL_FRAUD {
		if id, ok := lr.Entities[f.cfg.Node]; ok {
			f.fraud[id] = true
		}
	}
}
