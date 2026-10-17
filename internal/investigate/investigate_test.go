package investigate

import (
	"context"
	"fmt"
	"testing"

	"github.com/kanishka-sahoo/unfraud/internal/graph"
	"github.com/kanishka-sahoo/unfraud/internal/profile/tabformer"
)

type fixedGraph graph.Subgraph

func (g fixedGraph) Neighborhood(context.Context, string, string, int) (graph.Subgraph, error) {
	return graph.Subgraph(g), nil
}

func TestNetworkSaysHowFraudCardsConnect(t *testing.T) {
	two := 2
	sg := graph.Subgraph{Center: "c", Nodes: []graph.GNode{
		{ID: "c", Kind: "payer", Community: &two}, {ID: "m1", Kind: "payee"}, {ID: "m2", Kind: "payee"},
		{ID: "f1", Kind: "payer", Fraud: true}, {ID: "x", Kind: "payer"}, {ID: "m3", Kind: "payee"},
		{ID: "f2", Kind: "payer", Fraud: true},
	}, Edges: []graph.GEdge{{Source: "c", Target: "m1"}, {Source: "c", Target: "m2"}, {Source: "m1", Target: "f1"},
		{Source: "m2", Target: "f1"}, {Source: "m1", Target: "x"}, {Source: "x", Target: "m3"}, {Source: "m3", Target: "f2"}}}
	n, _, err := Lookup{Graph: fixedGraph(sg)}.Network(context.Background(), tabformer.Profile, "c", 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(n.FraudLinks); got != "[{f1 [m1 m2] } {f2 [] no shared merchant: it used a merchant that another linked card also used}]" {
		t.Errorf("fraud links %s", got)
	}
	if got := fmt.Sprint(n.TopMerchants); got != "[{m1 2} {m2 1}]" {
		t.Errorf("top merchants %s", got)
	}
	if n.OtherCards != 3 || n.FraudCards != 2 || n.Merchants != 3 {
		t.Errorf("counts %+v", n)
	}
}
