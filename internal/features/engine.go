// Package features computes a Profile's real-time features from its input
// stream. The same Engine runs in Accuracy Runs (to produce training data)
// and in the scorer, so training and serving features are identical by
// construction (ADR-0005).
package features

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/graph"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

const (
	// bucketsPerWindow sets the resolution of bucketed windows: a window of
	// length W is read as the W/24-long buckets ending with the current one,
	// so it reaches back between 23/24·W and W.
	bucketsPerWindow = 24
	// lastRetention bounds how long a previous value stays comparable for
	// SinceLast, Changed and Delta.
	lastRetention = 90 * 24 * time.Hour
	// pairRetention bounds NewPair's memory of Entity pairs.
	pairRetention = 90 * 24 * time.Hour
	// sweepEvery spaces out State.Expire calls in Event Time.
	sweepEvery = 24 * time.Hour
	// expireLag keeps expiry well behind the Event Time being processed.
	// Scorer workers on different partitions run slightly apart in Event
	// Time; one that is ahead must not delete state another still reads.
	// Expiring later never changes results: expired state is invisible.
	expireLag = 6 * time.Hour
	// guardRetention is how long, in Event Time, a record's feature updates
	// are remembered as applied. Redeliveries arrive within minutes.
	guardRetention = 7 * 24 * time.Hour
)

// Engine turns Events into feature vectors, updating State as it goes.
type Engine struct {
	st     State
	feats  []compiled
	cols   []string
	retain map[string]int64 // set key prefix -> member retention (ms)
	// hasFraudLabels is set when some feature counts released fraud Labels.
	hasFraudLabels bool
	graph          *graph.Graph // optional; its columns come last
	swept          int64
	started        bool

	qs []Query
	rs []Result
	us []Update
}

type compiled struct {
	profile.Feature
	num     int // schema index of Field when numeric, else -1
	cat     int // schema index of Field when categorical, else -1
	windows []window
	col     int // first output column
}

type window struct{ ms, bucket int64 }

// Option configures an Engine.
type Option func(*Engine) error

// WithGraph adds the Profile's attribute-graph features, computed in-process.
// Only a single ordered stream sees the whole graph, so this is for Accuracy
// Runs; live scorers read graph features published by graph-svc instead.
func WithGraph(p profile.Profile) Option {
	return func(e *Engine) error {
		if p.Graph == nil {
			return nil
		}
		g, err := graph.New(*p.Graph)
		if err != nil {
			return err
		}
		e.graph = g
		e.cols = append(e.cols, graph.Columns...)
		return nil
	}
}

// Neighborhood returns a Node's neighbourhood in the attribute graph; ok is
// false when the Profile has no graph.
func (e *Engine) Neighborhood(t int64, node string, hops, maxNodes int) (graph.Subgraph, bool) {
	if e.graph == nil {
		return graph.Subgraph{}, false
	}
	return e.graph.Neighborhood(t, node, hops, maxNodes), true
}

// New compiles a Profile's features.
func New(p profile.Profile, st State, opts ...Option) (*Engine, error) {
	e := &Engine{st: st, retain: map[string]int64{}}
	for _, f := range p.Features {
		if err := f.Validate(p.Schema); err != nil {
			return nil, fmt.Errorf("profile %s: %w", p.Name, err)
		}
		c := compiled{Feature: f, num: -1, cat: -1, col: len(e.cols)}
		if i, ok := p.Schema.NumIndex(f.Field); ok {
			c.num = i
		} else if i, ok := p.Schema.CatIndex(f.Field); ok {
			c.cat = i
		}
		for _, w := range f.Windows {
			ms := w.Milliseconds()
			if ms%bucketsPerWindow != 0 {
				return nil, fmt.Errorf("feature %s: window %v is not divisible into %d buckets", f.Name, w, bucketsPerWindow)
			}
			c.windows = append(c.windows, window{ms: ms, bucket: ms / bucketsPerWindow})
		}
		switch f.Kind {
		case profile.Distinct:
			k := setKey(f.Entity, f.Of, "")
			e.retain[k] = max(e.retain[k], slicesMax(c.windows))
		case profile.NewPair:
			k := setKey(f.Entity, f.Of, "")
			e.retain[k] = max(e.retain[k], pairRetention.Milliseconds())
		}
		e.hasFraudLabels = e.hasFraudLabels || f.Kind == profile.FraudLabels
		e.feats = append(e.feats, c)
		e.cols = append(e.cols, f.Columns()...)
	}
	for _, o := range opts {
		if err := o(e); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func slicesMax(ws []window) int64 {
	var m int64
	for _, w := range ws {
		m = max(m, w.ms)
	}
	return m
}

// Rings returns Rings found by the in-process graph since the last call.
func (e *Engine) Rings() []graph.Ring {
	if e.graph == nil {
		return nil
	}
	return e.graph.Rings()
}

// Columns names the values Process returns, in order.
func (e *Engine) Columns() []string { return e.cols }

// Process applies one input record. For an Event it returns the Event's
// features, computed from history before the Event; otherwise it returns nil.
func (e *Engine) Process(ctx context.Context, env *unfraudv1.Envelope) ([]float64, error) {
	t := env.EventTimeMs
	if !e.started {
		e.started, e.swept = true, t
	}
	var gv []float64
	if e.graph != nil {
		gv = e.graph.Process(env)
	}
	switch p := env.Payload.(type) {
	case *unfraudv1.Envelope_Event:
		out, err := e.event(ctx, t, p.Event)
		if err != nil || e.graph == nil {
			return out, err
		}
		copy(out[len(out)-len(gv):], gv)
		return out, nil
	case *unfraudv1.Envelope_LabelRelease:
		return nil, e.labelRelease(ctx, t, p.LabelRelease)
	case *unfraudv1.Envelope_Watermark:
		if t-e.swept >= sweepEvery.Milliseconds() {
			e.swept = t
			return nil, e.st.Expire(ctx, t-expireLag.Milliseconds())
		}
	}
	return nil, nil
}

func (e *Engine) event(ctx context.Context, t int64, ev *unfraudv1.Event) ([]float64, error) {
	// Updates depend only on the Event, never on what the reads return, so
	// both are planned first and exchanged with State in one call (one round
	// trip for Valkey). Reads still see the state from before the Event.
	e.qs, e.us = e.qs[:0], e.us[:0]
	start := make([]int, len(e.feats)) // first query index per feature, -1 if skipped
	seen := map[string]bool{}          // features sharing state update it once
	for i, f := range e.feats {
		start[i] = len(e.qs)
		id, ok := ev.Entities[f.Entity]
		if !ok {
			start[i] = -1
			continue
		}
		e.queries(t, f, id, ev)
		e.updates(t, f, id, ev, seen)
	}
	if cap(e.rs) < len(e.qs) {
		e.rs = make([]Result, len(e.qs))
	}
	e.rs = e.rs[:len(e.qs)]
	g := Guard{Key: "e|" + ev.EventId, ExpireMs: t + guardRetention.Milliseconds()}
	if err := e.st.Exchange(ctx, g, e.qs, e.rs, e.us); err != nil {
		return nil, err
	}

	out := make([]float64, len(e.cols))
	for i := range out {
		out[i] = math.NaN()
	}
	for i, f := range e.feats {
		if start[i] >= 0 {
			e.compute(t, f, ev, e.rs[start[i]:], out[f.col:])
		}
	}
	return out, nil
}

func bucketOf(t, size int64) int64 {
	q := t / size
	if t%size != 0 && t < 0 {
		q--
	}
	return q
}

func seriesKey(kind, entity, field string, bucket int64, id string) string {
	return kind + "|" + entity + "|" + field + "|" + strconv.FormatInt(bucket, 10) + "|" + id
}

func setKey(entity, of, id string) string { return "z|" + entity + "|" + of + "|" + id }

func lastKey(entity, field, id string) string { return "l|" + entity + "|" + field + "|" + id }

// queries appends the reads feature f needs, in the order compute consumes them.
func (e *Engine) queries(t int64, f compiled, id string, ev *unfraudv1.Event) {
	sum := func(kind, field string, w window) {
		cur := bucketOf(t, w.bucket)
		e.qs = append(e.qs, Query{Kind: QSum, Key: seriesKey(kind, f.Entity, field, w.bucket, id),
			From: cur - bucketsPerWindow + 1, To: cur})
	}
	switch f.Kind {
	case profile.Count:
		for _, w := range f.windows {
			sum("c", "", w)
		}
	case profile.Sum:
		for _, w := range f.windows {
			sum("s", f.Field, w)
		}
	case profile.RatioToMean:
		for _, w := range f.windows {
			sum("s", f.Field, w)
			sum("n", f.Field, w)
		}
	case profile.FraudLabels:
		for _, w := range f.windows {
			sum("f", "", w)
		}
	case profile.Distinct:
		for _, w := range f.windows {
			e.qs = append(e.qs, Query{Kind: QCountSince, Key: setKey(f.Entity, f.Of, id), From: t - w.ms})
		}
	case profile.NewPair:
		e.qs = append(e.qs, Query{Kind: QMember, Key: setKey(f.Entity, f.Of, id), Member: ev.Entities[f.Of]})
	case profile.SinceLast:
		e.qs = append(e.qs, Query{Kind: QLast, Key: lastKey(f.Entity, "", id)})
	case profile.Changed, profile.Delta:
		e.qs = append(e.qs, Query{Kind: QLast, Key: lastKey(f.Entity, f.Field, id)})
	}
}

// compute writes feature f's columns from its query results.
func (e *Engine) compute(t int64, f compiled, ev *unfraudv1.Event, rs []Result, out []float64) {
	switch f.Kind {
	case profile.Count, profile.Sum, profile.FraudLabels, profile.Distinct:
		for i := range f.windows {
			out[i] = rs[i].Num
		}
	case profile.RatioToMean:
		v := ev.Num[f.num]
		for i := range f.windows {
			sum, n := rs[2*i].Num, rs[2*i+1].Num
			if n > 0 && sum > 0 && !math.IsNaN(v) {
				out[i] = v / (sum / n)
			}
		}
	case profile.NewPair:
		if _, ok := ev.Entities[f.Of]; !ok {
			return
		}
		r := rs[0]
		out[0] = 1
		if r.OK && r.Time >= t-e.retain[setKey(f.Entity, f.Of, "")] {
			out[0] = 0
		}
	case profile.SinceLast:
		if rs[0].OK && rs[0].Time >= t-lastRetention.Milliseconds() {
			out[0] = float64(t-rs[0].Time) / 1000
		}
	case profile.Changed:
		cur, ok := fieldString(f, ev)
		if ok && rs[0].OK && rs[0].Time >= t-lastRetention.Milliseconds() {
			out[0] = b2f(cur != rs[0].Str)
		}
	case profile.Delta:
		cur := ev.Num[f.num]
		if !math.IsNaN(cur) && rs[0].OK && rs[0].Time >= t-lastRetention.Milliseconds() {
			prev, err := strconv.ParseFloat(rs[0].Str, 64)
			if err == nil {
				out[0] = cur - prev
			}
		}
	}
}

// updates appends the writes feature f makes for this Event. Features that
// share a key (for example Count and RatioToMean over the same window) write
// it once.
func (e *Engine) updates(t int64, f compiled, id string, ev *unfraudv1.Event, seen map[string]bool) {
	add := func(u Update) {
		k := string(rune('0'+u.Kind)) + u.Key
		if seen[k] {
			return
		}
		seen[k] = true
		e.us = append(e.us, u)
	}
	incr := func(kind, field string, w window, delta float64) {
		cur := bucketOf(t, w.bucket)
		add(Update{Kind: UIncr, Key: seriesKey(kind, f.Entity, field, w.bucket, id), Bucket: cur,
			Keep: bucketsPerWindow, Delta: delta, ExpireMs: (cur+1)*w.bucket + w.ms})
	}
	lastExpire := t + lastRetention.Milliseconds()
	switch f.Kind {
	case profile.Count:
		for _, w := range f.windows {
			incr("c", "", w, 1)
		}
	case profile.Sum, profile.RatioToMean:
		v := ev.Num[f.num]
		if math.IsNaN(v) {
			return
		}
		for _, w := range f.windows {
			incr("s", f.Field, w, v)
			if f.Kind == profile.RatioToMean {
				incr("n", f.Field, w, 1)
			}
		}
	case profile.Distinct, profile.NewPair:
		other, ok := ev.Entities[f.Of]
		if !ok {
			return
		}
		key := setKey(f.Entity, f.Of, id)
		add(Update{Kind: UTouch, Key: key, Member: other, Time: t,
			ExpireMs: t + e.retain[setKey(f.Entity, f.Of, "")]})
	case profile.SinceLast:
		add(Update{Kind: USetLast, Key: lastKey(f.Entity, "", id), Time: t, ExpireMs: lastExpire})
	case profile.Changed, profile.Delta:
		if cur, ok := fieldString(f, ev); ok {
			add(Update{Kind: USetLast, Key: lastKey(f.Entity, f.Field, id), Time: t, Str: cur, ExpireMs: lastExpire})
		}
	}
}

func (e *Engine) labelRelease(ctx context.Context, t int64, lr *unfraudv1.LabelRelease) error {
	if lr.Label != unfraudv1.Label_LABEL_FRAUD {
		return nil
	}
	if len(lr.Entities) == 0 && e.hasFraudLabels {
		// Written by a replayer from before Label Releases carried Entities.
		// Scoring on would silently zero every released-label feature.
		return fmt.Errorf("label release for %s has no entities; replay the input topic with the current replayer", lr.EventId)
	}
	e.us = e.us[:0]
	seen := map[string]bool{}
	for _, f := range e.feats {
		if f.Kind != profile.FraudLabels {
			continue
		}
		id, ok := lr.Entities[f.Entity]
		if !ok {
			continue
		}
		for _, w := range f.windows {
			cur := bucketOf(t, w.bucket)
			k := seriesKey("f", f.Entity, "", w.bucket, id)
			if seen[k] {
				continue
			}
			seen[k] = true
			e.us = append(e.us, Update{Kind: UIncr, Key: k, Bucket: cur, Keep: bucketsPerWindow, Delta: 1,
				ExpireMs: (cur+1)*w.bucket + w.ms})
		}
	}
	return e.st.Apply(ctx, Guard{Key: "l|" + lr.EventId, ExpireMs: t + guardRetention.Milliseconds()}, e.us)
}

// fieldString renders f's Field for comparison with its previous value.
func fieldString(f compiled, ev *unfraudv1.Event) (string, bool) {
	if f.cat >= 0 {
		v := ev.Cat[f.cat]
		return v, v != ""
	}
	v := ev.Num[f.num]
	if math.IsNaN(v) {
		return "", false
	}
	return strconv.FormatFloat(v, 'g', -1, 64), true
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
