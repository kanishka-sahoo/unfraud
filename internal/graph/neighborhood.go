package graph

import (
	"sort"
	"strings"
)

// Subgraph is a Node's neighbourhood, for the web app's graph explorer.
type Subgraph struct {
	Center string `json:"center"`
	// AsOf is the Event Time the graph has reached, in Unix milliseconds;
	// the neighbourhood is the one at that time, not at any Event's.
	AsOf      int64   `json:"as_of_ms,omitempty"`
	Nodes     []GNode `json:"nodes"`
	Edges     []GEdge `json:"edges"`
	Truncated bool    `json:"truncated"`
}

type GNode struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`  // the Node or Link Entity type, e.g. payer, payee
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

// Neighborhood returns a Node's neighbourhood in the attribute graph over
// the current window: Nodes and the Link attributes they share.
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
	// Super-nodes are not linked through (MaxDegree), so their other Nodes
	// are not part of the neighbourhood either.
	nodes, edges, truncated := bfs(node, hops, maxNodes, func(n string) []string {
		if len(g.degree[n]) > g.cfg.MaxDegree {
			return nil
		}
		var out []string
		for m := range adj[n] {
			out = append(out, m)
		}
		return out
	})
	sg := Subgraph{Center: node, Truncated: truncated}
	for _, n := range nodes {
		gn := GNode{ID: n, Kind: g.cfg.Node}
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
