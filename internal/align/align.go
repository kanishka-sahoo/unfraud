// Package align releases records from several partitions in Event Time
// order without letting any partition run ahead of another that still has
// unread data. Scorer workers share feature state, so a partition that ran
// hours ahead would let other partitions' Events see its future (ADR-0016).
package align

import (
	"math"
	"sync"
)

// Item is one buffered record.
type Item struct {
	Partition int32
	Offset    int64
	TimeMs    int64
	Sequence  int64 // breaks Event Time ties across partitions; 0 if unknown
	Value     any
}

type partition struct {
	queue    []Item
	lastTime int64 // Event Time of the last released Item
	next     int64 // offset after the last buffered Item
	hwm      int64 // high watermark from the last fetch
	known    bool
}

// Buffer is safe for concurrent use.
type Buffer struct {
	mu    sync.Mutex
	parts map[int32]*partition
}

func NewBuffer() *Buffer { return &Buffer{parts: map[int32]*partition{}} }

// Add buffers a partition's fetched Items (in offset order) and records the
// partition's high watermark.
func (b *Buffer) Add(p int32, items []Item, hwm int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.parts[p]
	if st == nil {
		st = &partition{lastTime: math.MinInt64}
		b.parts[p] = st
	}
	st.queue = append(st.queue, items...)
	if n := len(items); n > 0 {
		st.next = items[n-1].Offset + 1
	}
	st.hwm, st.known = hwm, true
}

// Drop forgets a partition, for example after it is revoked. Its unreleased
// Items were never committed and will be redelivered to its next owner.
func (b *Buffer) Drop(p int32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.parts, p)
}

// Queued reports how many Items wait in a partition.
func (b *Buffer) Queued(p int32) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if st := b.parts[p]; st != nil {
		return len(st.queue)
	}
	return 0
}

// frontier is the latest Event Time up to which a partition's records are
// known: past it, unread records could still arrive.
func (st *partition) frontier() int64 {
	caughtUp := st.next >= st.hwm
	switch {
	case caughtUp:
		return math.MaxInt64
	case len(st.queue) > 0:
		return st.queue[len(st.queue)-1].TimeMs
	default:
		return st.lastTime
	}
}

// Next releases up to max Items in (Event Time, sequence, partition, offset) order,
// none later than the earliest frontier across partitions.
func (b *Buffer) Next(max int) []Item {
	b.mu.Lock()
	defer b.mu.Unlock()
	limit := int64(math.MaxInt64)
	for _, st := range b.parts {
		limit = min(limit, st.frontier())
	}
	var out []Item
	for len(out) < max {
		var best *partition
		for _, st := range b.parts {
			if len(st.queue) == 0 || st.queue[0].TimeMs > limit {
				continue
			}
			if best == nil || less(st.queue[0], best.queue[0]) {
				best = st
			}
		}
		if best == nil {
			break
		}
		it := best.queue[0]
		best.queue = best.queue[1:]
		best.lastTime = it.TimeMs
		out = append(out, it)
	}
	return out
}

func less(a, b Item) bool {
	if a.TimeMs != b.TimeMs {
		return a.TimeMs < b.TimeMs
	}
	if a.Sequence != b.Sequence {
		return a.Sequence < b.Sequence
	}
	if a.Partition != b.Partition {
		return a.Partition < b.Partition
	}
	return a.Offset < b.Offset
}

// Drain releases everything buffered, ignoring frontiers, partition by
// partition. It exists to measure what alignment buys (scorer -align=false).
func (b *Buffer) Drain() []Item {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Item
	for _, st := range b.parts {
		out = append(out, st.queue...)
		if n := len(st.queue); n > 0 {
			st.lastTime = st.queue[n-1].TimeMs
		}
		st.queue = nil
	}
	return out
}
