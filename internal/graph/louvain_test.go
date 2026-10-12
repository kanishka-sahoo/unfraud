package graph

import (
	"math/rand"
	"slices"
	"testing"
)

func build(n int, edges [][2]int) [][]wedge {
	adj := make([][]wedge, n)
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], wedge{e[1], 1})
		adj[e[1]] = append(adj[e[1]], wedge{e[0], 1})
	}
	return adj
}

func clique(nodes ...int) [][2]int {
	var es [][2]int
	for i, a := range nodes {
		for _, b := range nodes[i+1:] {
			es = append(es, [2]int{a, b})
		}
	}
	return es
}

func TestSplitsTwoCliquesJoinedByOneEdge(t *testing.T) {
	edges := append(clique(0, 1, 2, 3, 4), clique(5, 6, 7, 8, 9)...)
	edges = append(edges, [2]int{4, 5})
	got := louvain(build(10, edges))
	want := []int{0, 0, 0, 0, 0, 1, 1, 1, 1, 1}
	if !slices.Equal(got, want) {
		t.Fatalf("communities %v, want %v", got, want)
	}
}

func TestSeparateComponentsStaySeparate(t *testing.T) {
	got := louvain(build(6, [][2]int{{0, 1}, {1, 2}, {3, 4}, {4, 5}}))
	if got[0] != got[2] || got[3] != got[5] || got[0] == got[3] {
		t.Fatalf("communities %v", got)
	}
}

func TestIsolatedNodesAreTheirOwnCommunities(t *testing.T) {
	got := louvain(build(3, nil))
	if !slices.Equal(got, []int{0, 1, 2}) {
		t.Fatalf("communities %v", got)
	}
}

func TestDeterministic(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	var edges [][2]int
	for range 3000 {
		edges = append(edges, [2]int{r.Intn(800), r.Intn(800)})
	}
	adj := build(800, edges)
	first := louvain(adj)
	for range 5 {
		if !slices.Equal(louvain(adj), first) {
			t.Fatal("louvain gave different partitions for the same graph")
		}
	}
}
