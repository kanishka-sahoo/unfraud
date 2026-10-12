// Package lgbm evaluates LightGBM binary models saved in LightGBM's text
// format (v3 and v4). It ports LightGBM's decision rules exactly, including
// how NaN and zero values are routed, so scores match Python to within
// floating-point summation error (ADR-0008).
package lgbm

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

// Model is a binary-classification tree ensemble.
type Model struct {
	FeatureNames []string
	Trees        []Tree
	sigmoid      float64
	maxDepth     int
}

// Tree is one regression tree. Internal nodes are indexed from 0; a negative
// child c refers to leaf ^c.
type Tree struct {
	SplitFeature  []int
	Threshold     []float64
	DecisionType  []uint8
	LeftChild     []int
	RightChild    []int
	LeafValue     []float64
	LeafCount     []float64
	InternalCount []float64
	CatBoundaries []int
	CatThreshold  []uint32

	expected float64 // TreeSHAP expected value (bias)
}

// Decision type bits, from LightGBM's tree.h.
const (
	categoricalMask = 1
	defaultLeftMask = 2
	missingNone     = 0
	missingZero     = 1
	missingNaN      = 2
	zeroThreshold   = 1e-35
)

// Load reads a model file.
func Load(path string) (*Model, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Parse reads a model in LightGBM's text format.
func Parse(r io.Reader) (*Model, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<28) // tree lines can be long
	m := &Model{sigmoid: 1}
	header := map[string]string{}
	var tree map[string]string

	finish := func() error {
		if tree == nil {
			return nil
		}
		t, err := parseTree(tree)
		if err != nil {
			return fmt.Errorf("tree %d: %w", len(m.Trees), err)
		}
		m.Trees = append(m.Trees, t)
		tree = nil
		return nil
	}

	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "end of trees":
			if err := finish(); err != nil {
				return nil, err
			}
			return m, m.applyHeader(header)
		case strings.HasPrefix(line, "Tree="):
			if err := finish(); err != nil {
				return nil, err
			}
			tree = map[string]string{}
		case line == "":
		default:
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			if tree != nil {
				tree[k] = v
			} else {
				header[k] = v
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("lgbm: missing \"end of trees\"")
}

func (m *Model) applyHeader(h map[string]string) error {
	switch h["version"] {
	case "v3", "v4":
	default:
		return fmt.Errorf("lgbm: unsupported model version %q", h["version"])
	}
	if n := h["num_class"]; n != "1" {
		return fmt.Errorf("lgbm: only binary models are supported, num_class=%s", n)
	}
	obj := strings.Fields(h["objective"])
	if len(obj) == 0 || obj[0] != "binary" {
		return fmt.Errorf("lgbm: only the binary objective is supported, got %q", h["objective"])
	}
	for _, o := range obj[1:] {
		if v, ok := strings.CutPrefix(o, "sigmoid:"); ok {
			s, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("lgbm: objective sigmoid: %w", err)
			}
			m.sigmoid = s
		}
	}
	m.FeatureNames = strings.Fields(h["feature_names"])
	for i := range m.Trees {
		t := &m.Trees[i]
		if len(t.SplitFeature) > 0 && (len(t.InternalCount) != len(t.SplitFeature) || len(t.LeafCount) != len(t.LeafValue)) {
			return fmt.Errorf("lgbm: tree %d lacks node counts", i)
		}
		t.expected = t.expectedValue()
		if len(t.SplitFeature) > 0 {
			m.maxDepth = max(m.maxDepth, t.depth())
		}
		for _, f := range t.SplitFeature {
			if f >= len(m.FeatureNames) {
				return fmt.Errorf("lgbm: tree %d splits on feature %d of %d", i, f, len(m.FeatureNames))
			}
		}
	}
	return nil
}

func parseTree(kv map[string]string) (Tree, error) {
	var t Tree
	var err error
	if kv["is_linear"] == "1" {
		return t, fmt.Errorf("linear trees are not supported")
	}
	numLeaves, err := strconv.Atoi(kv["num_leaves"])
	if err != nil {
		return t, fmt.Errorf("num_leaves: %w", err)
	}
	if t.LeafValue, err = floats(kv["leaf_value"]); err != nil {
		return t, fmt.Errorf("leaf_value: %w", err)
	}
	if len(t.LeafValue) != numLeaves {
		return t, fmt.Errorf("%d leaf values for %d leaves", len(t.LeafValue), numLeaves)
	}
	t.LeafCount, _ = floats(kv["leaf_count"])
	if numLeaves == 1 {
		return t, nil
	}
	ints := func(key string) ([]int, error) {
		f := strings.Fields(kv[key])
		out := make([]int, len(f))
		for i, s := range f {
			v, err := strconv.Atoi(s)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			out[i] = v
		}
		return out, nil
	}
	if t.SplitFeature, err = ints("split_feature"); err != nil {
		return t, err
	}
	if t.LeftChild, err = ints("left_child"); err != nil {
		return t, err
	}
	if t.RightChild, err = ints("right_child"); err != nil {
		return t, err
	}
	dt, err := ints("decision_type")
	if err != nil {
		return t, err
	}
	t.DecisionType = make([]uint8, len(dt))
	for i, d := range dt {
		t.DecisionType[i] = uint8(d)
	}
	if t.Threshold, err = floats(kv["threshold"]); err != nil {
		return t, fmt.Errorf("threshold: %w", err)
	}
	t.InternalCount, _ = floats(kv["internal_count"])
	if kv["num_cat"] != "" && kv["num_cat"] != "0" {
		if t.CatBoundaries, err = ints("cat_boundaries"); err != nil {
			return t, err
		}
		cats, err := ints("cat_threshold")
		if err != nil {
			return t, err
		}
		t.CatThreshold = make([]uint32, len(cats))
		for i, c := range cats {
			t.CatThreshold[i] = uint32(c)
		}
	}
	n := numLeaves - 1
	if len(t.SplitFeature) != n || len(t.LeftChild) != n || len(t.RightChild) != n ||
		len(t.DecisionType) != n || len(t.Threshold) != n {
		return t, fmt.Errorf("inconsistent node arrays for %d internal nodes", n)
	}
	return t, nil
}

func floats(s string) ([]float64, error) {
	f := strings.Fields(s)
	out := make([]float64, len(f))
	for i, v := range f {
		x, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, err
		}
		out[i] = x
	}
	return out, nil
}

// Raw returns the model's raw score (log-odds) for one feature vector.
func (m *Model) Raw(x []float64) float64 {
	var sum float64
	for i := range m.Trees {
		sum += m.Trees[i].LeafValue[m.Trees[i].Leaf(x)]
	}
	return sum
}

// Probability returns the fraud probability for one feature vector.
func (m *Model) Probability(x []float64) float64 {
	return 1 / (1 + math.Exp(-m.sigmoid*m.Raw(x)))
}

// Leaf returns the index of the leaf x falls into.
func (t *Tree) Leaf(x []float64) int {
	if len(t.SplitFeature) == 0 {
		return 0
	}
	node := 0
	for node >= 0 {
		node = t.next(node, x[t.SplitFeature[node]])
	}
	return ^node
}

// next is LightGBM's Tree::Decision.
func (t *Tree) next(node int, v float64) int {
	dt := t.DecisionType[node]
	if dt&categoricalMask != 0 {
		if math.IsNaN(v) || int(v) < 0 {
			return t.RightChild[node]
		}
		cat := int(t.Threshold[node])
		lo, hi := t.CatBoundaries[cat], t.CatBoundaries[cat+1]
		if inBitset(t.CatThreshold[lo:hi], int(v)) {
			return t.LeftChild[node]
		}
		return t.RightChild[node]
	}
	missing := (dt >> 2) & 3
	if math.IsNaN(v) && missing != missingNaN {
		v = 0
	}
	if (missing == missingZero && v >= -zeroThreshold && v <= zeroThreshold) ||
		(missing == missingNaN && math.IsNaN(v)) {
		if dt&defaultLeftMask != 0 {
			return t.LeftChild[node]
		}
		return t.RightChild[node]
	}
	if v <= t.Threshold[node] {
		return t.LeftChild[node]
	}
	return t.RightChild[node]
}

func inBitset(bits []uint32, pos int) bool {
	i := pos / 32
	if i >= len(bits) {
		return false
	}
	return bits[i]>>(pos%32)&1 == 1
}
