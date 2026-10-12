package align

import (
	"fmt"
	"strings"
	"testing"
)

func items(p int32, firstOffset int64, times ...int64) []Item {
	out := make([]Item, len(times))
	for i, t := range times {
		out[i] = Item{Partition: p, Offset: firstOffset + int64(i), TimeMs: t}
	}
	return out
}

func show(its []Item) string {
	var s []string
	for _, it := range its {
		s = append(s, fmt.Sprintf("p%d@%d", it.Partition, it.TimeMs))
	}
	return strings.Join(s, " ")
}

func TestReleasesInEventTimeOrderAcrossPartitions(t *testing.T) {
	b := NewBuffer()
	b.Add(0, items(0, 0, 1, 4, 9), 3) // caught up after these
	b.Add(1, items(1, 0, 2, 3, 10), 3)
	if got, want := show(b.Next(100)), "p0@1 p1@2 p1@3 p0@4 p0@9 p1@10"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestPartitionWithUnreadDataHoldsOthersBack(t *testing.T) {
	b := NewBuffer()
	b.Add(0, items(0, 0, 1, 2, 50, 60), 4) // caught up
	b.Add(1, items(1, 0, 3, 5), 100)       // 98 more records unread
	if got, want := show(b.Next(100)), "p0@1 p0@2 p1@3 p1@5"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	// Partition 1's queue is empty but it has unread data: nothing past its
	// last released time may go.
	if got := b.Next(100); len(got) != 0 {
		t.Fatalf("released %s while partition 1 has unread data", show(got))
	}
	b.Add(1, items(1, 2, 55, 70), 4) // now caught up
	if got, want := show(b.Next(100)), "p0@50 p1@55 p0@60 p1@70"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestMaxBoundsAChunk(t *testing.T) {
	b := NewBuffer()
	b.Add(0, items(0, 0, 1, 2, 3), 3)
	if got := b.Next(2); len(got) != 2 {
		t.Fatalf("released %d, want 2", len(got))
	}
	if got := b.Next(2); show(got) != "p0@3" {
		t.Fatalf("got %s", show(got))
	}
}

func TestDroppedPartitionNoLongerHoldsBack(t *testing.T) {
	b := NewBuffer()
	b.Add(0, items(0, 0, 1, 9), 2)
	b.Add(1, items(1, 0, 5), 50)
	b.Next(100)
	if got := b.Next(100); len(got) != 0 {
		t.Fatalf("released %s", show(got))
	}
	b.Drop(1)
	if got := show(b.Next(100)); got != "p0@9" {
		t.Fatalf("after drop got %s", got)
	}
}

func TestSequenceBreaksEventTimeTies(t *testing.T) {
	b := NewBuffer()
	b.Add(0, []Item{{Partition: 0, Offset: 0, TimeMs: 5, Sequence: 3}}, 1)
	b.Add(1, []Item{{Partition: 1, Offset: 0, TimeMs: 5, Sequence: 2}, {Partition: 1, Offset: 1, TimeMs: 5, Sequence: 4}}, 2)
	var seq []int64
	for _, it := range b.Next(10) {
		seq = append(seq, it.Sequence)
	}
	if fmt.Sprint(seq) != "[2 3 4]" {
		t.Fatalf("order %v, want [2 3 4]", seq)
	}
}
