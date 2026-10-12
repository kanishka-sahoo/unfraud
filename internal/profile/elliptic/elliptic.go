// Package elliptic adapts the Elliptic Bitcoin transaction graph. Nodes are
// transactions and edges are payment flows; every edge joins two nodes of the
// same timestep, so a timestep's Links are emitted after all of its Events.
package elliptic

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/csvx"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

const (
	name = "elliptic"
	dir  = "elliptic_bitcoin_dataset"
	// Timesteps are about two weeks apart. The dataset gives no calendar
	// dates, so timestep t is placed at base + (t-1) * stepDuration.
	stepDuration = 14 * 24 * time.Hour
	localCount   = 93 // local features after the timestep column
	aggCount     = 72
	entityType   = "tx"
)

// base is aligned to a multiple of stepDuration since the Unix epoch, so each
// timestep falls in its own watermark bucket.
var base = time.UnixMilli(0).UTC().Add(1200 * stepDuration) // 2015-12-30

// schema puts the timestep first, followed by the 93 local and 72 aggregated
// features, matching the file's column order.
var schema = profile.NewSchema(func() []string {
	n := make([]string, 0, 1+localCount+aggCount)
	n = append(n, "timestep")
	for i := 1; i <= localCount; i++ {
		n = append(n, fmt.Sprintf("local_%d", i))
	}
	for i := 1; i <= aggCount; i++ {
		n = append(n, fmt.Sprintf("agg_%d", i))
	}
	return n
}(), nil)

var Profile = profile.Profile{
	Name:              name,
	LabelDelay:        5 * stepDuration,
	WatermarkInterval: stepDuration,
	Schema:            schema,
	Open:              open,
}

type source struct {
	feats   *csvx.Reader
	labels  map[string]unfraudv1.Label
	edges   map[string][]string // src -> dsts, in file order
	step    int                 // timestep of the Events read so far
	srcs    []string            // Events of the current timestep, in order
	pending []profile.Record    // Links waiting to be returned
	next    *profile.Record     // first Event of the following timestep
	done    bool
}

func open(root string) (profile.Source, error) {
	d := filepath.Join(root, dir)
	labels, err := readLabels(filepath.Join(d, "elliptic_txs_classes.csv"))
	if err != nil {
		return nil, err
	}
	edges, err := readEdges(filepath.Join(d, "elliptic_txs_edgelist.csv"))
	if err != nil {
		return nil, err
	}
	feats, err := csvx.Open(filepath.Join(d, "elliptic_txs_features.csv"), false)
	if err != nil {
		return nil, err
	}
	return &source{feats: feats, labels: labels, edges: edges}, nil
}

func readLabels(path string) (map[string]unfraudv1.Label, error) {
	r, err := csvx.Open(path, true)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	c, err := r.Cols("txId", "class")
	if err != nil {
		return nil, err
	}
	m := make(map[string]unfraudv1.Label, 210_000)
	for {
		row, err := r.Read()
		if err == io.EOF {
			return m, nil
		}
		if err != nil {
			return nil, err
		}
		switch row[c[1]] {
		case "1":
			m[row[c[0]]] = unfraudv1.Label_LABEL_FRAUD
		case "2":
			m[row[c[0]]] = unfraudv1.Label_LABEL_LEGIT
		case "unknown":
		default:
			return nil, fmt.Errorf("%s line %d: unexpected class %q", path, r.Line(), row[c[1]])
		}
	}
}

func readEdges(path string) (map[string][]string, error) {
	r, err := csvx.Open(path, true)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	c, err := r.Cols("txId1", "txId2")
	if err != nil {
		return nil, err
	}
	m := make(map[string][]string, 210_000)
	for {
		row, err := r.Read()
		if err == io.EOF {
			return m, nil
		}
		if err != nil {
			return nil, err
		}
		m[row[c[0]]] = append(m[row[c[0]]], row[c[1]])
	}
}

func stepTime(step int) int64 {
	return base.Add(time.Duration(step-1) * stepDuration).UnixMilli()
}

func (s *source) Next() (profile.Record, error) {
	for {
		if len(s.pending) > 0 {
			rec := s.pending[0]
			s.pending = s.pending[1:]
			return rec, nil
		}
		if s.next != nil {
			rec := *s.next
			s.next = nil
			return rec, nil
		}
		if s.done {
			return profile.Record{}, io.EOF
		}
		rec, step, err := s.readEvent()
		if err == io.EOF {
			s.done = true
			s.closeStep()
			continue
		}
		if err != nil {
			return profile.Record{}, err
		}
		if s.step != 0 && step != s.step {
			s.closeStep()
			s.next = &rec
		}
		s.step = step
		s.srcs = append(s.srcs, rec.Key)
		if s.next == nil {
			return rec, nil
		}
	}
}

// closeStep queues the Links whose source is an Event of the current timestep.
func (s *source) closeStep() {
	t := stepTime(s.step)
	for _, src := range s.srcs {
		for _, dst := range s.edges[src] {
			env := profile.NewLink(name, t,
				&unfraudv1.EntityRef{Type: entityType, Id: src},
				&unfraudv1.EntityRef{Type: entityType, Id: dst})
			s.pending = append(s.pending, profile.Record{Env: env, Key: src})
		}
	}
	s.srcs = s.srcs[:0]
}

func (s *source) readEvent() (profile.Record, int, error) {
	row, err := s.feats.Read()
	if err != nil {
		return profile.Record{}, 0, err
	}
	if len(row) != 1+len(schema.Num) {
		return profile.Record{}, 0, fmt.Errorf("elliptic features line %d: %d columns, want %d", s.feats.Line(), len(row), 1+len(schema.Num))
	}
	step, err := strconv.Atoi(row[1])
	if err != nil {
		return profile.Record{}, 0, fmt.Errorf("elliptic features line %d: timestep: %w", s.feats.Line(), err)
	}
	num := make([]float64, len(schema.Num))
	num[0] = float64(step)
	for i := 1; i < len(num); i++ {
		v, err := strconv.ParseFloat(row[1+i], 64)
		if err != nil {
			return profile.Record{}, 0, fmt.Errorf("elliptic features line %d: %s: %w", s.feats.Line(), schema.Num[i], err)
		}
		num[i] = v
	}
	id := strings.Clone(row[0])
	ev := &unfraudv1.Event{EventId: id, Entities: map[string]string{entityType: id}, SchemaId: schema.ID, Num: num}
	return profile.Record{Env: profile.NewEvent(name, stepTime(step), ev), Key: id, Label: s.labels[id]}, step, nil
}

func (s *source) Close() error { return s.feats.Close() }
