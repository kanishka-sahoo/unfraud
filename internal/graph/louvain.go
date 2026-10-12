package graph

import (
	"maps"
	"slices"
)

// louvain partitions an undirected weighted graph into Communities by greedy
// modularity optimisation (Blondel et al. 2008): nodes move to the
// neighbouring community with the best modularity gain until nothing moves,
// then communities collapse into nodes and the process repeats.
//
// It is deterministic: nodes are visited in index order, candidate
// communities in id order, and a node moves only for a strictly better gain
// (so ties keep it where it is, then favour the lowest id). Accuracy Runs
// therefore reproduce exactly.
//
// adj[i] lists i's neighbours with edge weights; each undirected edge appears
// in both lists. It returns a community id per node, numbered from 0 in order
// of first appearance.
func louvain(adj [][]wedge) []int {
	n := len(adj)
	comm := make([]int, n)
	for i := range comm {
		comm[i] = i
	}
	g := adj
	// member[i] is the current super-node of original node i.
	member := make([]int, n)
	for i := range member {
		member[i] = i
	}
	for {
		moved, part := louvainLevel(g)
		if !moved {
			break
		}
		part, k := renumber(part)
		for i := range member {
			member[i] = part[member[i]]
		}
		g = aggregate(g, part, k)
	}
	out, _ := renumber(member)
	copy(comm, out)
	return comm
}

type wedge struct {
	to int
	w  float64
}

// louvainLevel runs local moves on g and returns whether any node moved and
// each node's community.
func louvainLevel(g [][]wedge) (bool, []int) {
	n := len(g)
	comm := make([]int, n)
	k := make([]float64, n)   // weighted degree
	tot := make([]float64, n) // total degree per community
	var m2 float64
	for i, es := range g {
		comm[i] = i
		for _, e := range es {
			k[i] += e.w
		}
		tot[i] = k[i]
		m2 += k[i]
	}
	if m2 == 0 {
		return false, comm
	}
	moved := false
	links := make(map[int]float64)
	var cands []int
	for improved := true; improved; {
		improved = false
		for i := 0; i < n; i++ {
			own := comm[i]
			clear(links)
			for _, e := range g[i] {
				if e.to != i {
					links[comm[e.to]] += e.w
				}
			}
			tot[own] -= k[i]
			cands = cands[:0]
			for c := range links {
				cands = append(cands, c)
			}
			slices.Sort(cands)
			best, bestGain := own, links[own]-tot[own]*k[i]/m2
			for _, c := range cands {
				if gain := links[c] - tot[c]*k[i]/m2; gain > bestGain+1e-12 {
					best, bestGain = c, gain
				}
			}
			tot[best] += k[i]
			if best != own {
				comm[i] = best
				improved, moved = true, true
			}
		}
	}
	return moved, comm
}

// renumber maps community ids to 0..k-1 in order of first appearance.
func renumber(part []int) ([]int, int) {
	ids := map[int]int{}
	out := make([]int, len(part))
	for i, c := range part {
		id, ok := ids[c]
		if !ok {
			id = len(ids)
			ids[c] = id
		}
		out[i] = id
	}
	return out, len(ids)
}

// aggregate collapses each community into one node; edges inside a community
// become a self-loop carrying their weight.
func aggregate(g [][]wedge, part []int, k int) [][]wedge {
	w := make([]map[int]float64, k)
	for i := range w {
		w[i] = map[int]float64{}
	}
	for i, es := range g {
		for _, e := range es {
			w[part[i]][part[e.to]] += e.w
		}
	}
	out := make([][]wedge, k)
	for c := range w {
		// Sorted neighbour order keeps the next level deterministic.
		for _, to := range slices.Sorted(maps.Keys(w[c])) {
			out[c] = append(out[c], wedge{to: to, w: w[c][to]})
		}
	}
	return out
}
