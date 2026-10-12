package graph

import (
	"fmt"
	"math"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

// Flow describes Events by the flow Links the dataset supplies (Elliptic).
// An Event's Links are complete only when its period closes, so Flow
// buffers a period's Events and Links and, when a Watermark closes the
// period, returns graph features for each of its Events: the input to
// Revisions (ADR-0003).
type Flow struct {
	cfg     profile.FlowConfig
	idx     []int // schema index of each aggregated feature
	cols    []string
	nodes   map[string]int // node id -> position in this period
	ids     []string
	vals    [][]float64
	links   [][2]string   // the period's Links, resolved when it closes
	out     map[int][]int // node -> nodes it pays
	in      map[int][]int
	period  int64 // Event Time of the buffered period
	pending []Revised
}

// Revised is one Event's graph features, available once its period closed.
type Revised struct {
	EventID  string
	PeriodMs int64 // Event Time of the period
	ClosedMs int64 // Watermark that closed it
	Values   []float64
}

func NewFlow(cfg profile.FlowConfig, schema *profile.Schema) (*Flow, error) {
	f := &Flow{cfg: cfg, cols: []string{"flow_in", "flow_out", "flow_in2", "flow_out2"}}
	for _, n := range cfg.Aggregate {
		i, ok := schema.NumIndex(n)
		if !ok {
			return nil, fmt.Errorf("flow graph: feature %q not in schema", n)
		}
		f.idx = append(f.idx, i)
		f.cols = append(f.cols, "nbr_mean_"+n)
	}
	f.reset()
	return f, nil
}

func (f *Flow) reset() {
	f.nodes, f.ids, f.vals, f.links = map[string]int{}, nil, nil, nil
	f.out, f.in = map[int][]int{}, map[int][]int{}
}

// Columns names Revised.Values.
func (f *Flow) Columns() []string { return f.cols }

// Process buffers Events and Links; a Watermark at or after the buffered
// period closes it.
func (f *Flow) Process(env *unfraudv1.Envelope) {
	switch p := env.Payload.(type) {
	case *unfraudv1.Envelope_Event:
		id, ok := p.Event.Entities[f.cfg.Node]
		if !ok {
			return
		}
		if _, seen := f.nodes[id]; seen {
			return
		}
		v := make([]float64, len(f.idx))
		for j, i := range f.idx {
			v[j] = p.Event.Num[i]
		}
		f.nodes[id] = len(f.ids)
		f.ids = append(f.ids, p.Event.EventId)
		f.vals = append(f.vals, v)
		f.period = env.EventTimeMs
	case *unfraudv1.Envelope_Link:
		// Within a period, a Link may arrive before its Events when several
		// partitions are merged, so Links are resolved only at close.
		f.links = append(f.links, [2]string{p.Link.Src.Id, p.Link.Dst.Id})
	case *unfraudv1.Envelope_Watermark:
		if len(f.ids) > 0 && env.EventTimeMs >= f.period {
			f.close(env.EventTimeMs)
		}
	}
}

func (f *Flow) close(closed int64) {
	for _, l := range f.links {
		src, sok := f.nodes[l[0]]
		dst, dok := f.nodes[l[1]]
		if sok && dok && src != dst {
			f.out[src] = append(f.out[src], dst)
			f.in[dst] = append(f.in[dst], src)
		}
	}
	n := len(f.ids)
	for i := 0; i < n; i++ {
		row := make([]float64, len(f.cols))
		row[0], row[1] = float64(len(f.in[i])), float64(len(f.out[i]))
		row[2], row[3] = float64(f.reach(i, f.in)), float64(f.reach(i, f.out))
		nbrs := append(append([]int(nil), f.in[i]...), f.out[i]...)
		for j := range f.idx {
			if len(nbrs) == 0 {
				row[4+j] = math.NaN()
				continue
			}
			var s float64
			for _, k := range nbrs {
				s += f.vals[k][j]
			}
			row[4+j] = s / float64(len(nbrs))
		}
		f.pending = append(f.pending, Revised{EventID: f.ids[i], PeriodMs: f.period, ClosedMs: closed, Values: row})
	}
	f.reset()
}

// reach counts distinct nodes exactly two hops away along adj.
func (f *Flow) reach(i int, adj map[int][]int) int {
	seen := map[int]bool{i: true}
	for _, j := range adj[i] {
		seen[j] = true
	}
	two := 0
	for _, j := range adj[i] {
		for _, k := range adj[j] {
			if !seen[k] {
				seen[k] = true
				two++
			}
		}
	}
	return two
}

// Revised returns the graph features of periods closed since the last call.
func (f *Flow) Revised() []Revised {
	r := f.pending
	f.pending = nil
	return r
}
