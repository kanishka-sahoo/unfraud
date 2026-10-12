package lgbm

// Contributions computes per-feature SHAP values with path-dependent
// TreeSHAP, ported from LightGBM's Tree::TreeSHAP (itself from Lundberg et
// al.), using node data counts as cover. phi must have len(FeatureNames)+1
// entries; the last receives the expected value (bias). The values sum to
// Raw(x).
func (m *Model) Contributions(x, phi []float64) {
	clear(phi)
	bias := len(phi) - 1
	d := m.maxDepth + 2
	path := make([]pathElement, d*(d+1)/2)
	for i := range m.Trees {
		t := &m.Trees[i]
		phi[bias] += t.expected
		if len(t.SplitFeature) > 0 {
			t.shap(x, phi, 0, 0, path, 1, 1, -1)
		}
	}
}

type pathElement struct {
	feature      int
	zeroFraction float64
	oneFraction  float64
	pweight      float64
}

func (t *Tree) expectedValue() float64 {
	if len(t.SplitFeature) == 0 {
		return t.LeafValue[0]
	}
	total := t.InternalCount[0]
	var v float64
	for i, lv := range t.LeafValue {
		v += t.LeafCount[i] / total * lv
	}
	return v
}

func (t *Tree) count(node int) float64 {
	if node >= 0 {
		return t.InternalCount[node]
	}
	return t.LeafCount[^node]
}

// depth returns the number of internal nodes on the longest root-leaf path.
func (t *Tree) depth() int {
	var walk func(node int) int
	walk = func(node int) int {
		if node < 0 {
			return 0
		}
		return 1 + max(walk(t.LeftChild[node]), walk(t.RightChild[node]))
	}
	return walk(0)
}

func (t *Tree) shap(x, phi []float64, node, depth int, parent []pathElement,
	parentZero, parentOne float64, parentFeature int) {
	// The path for this node lives just after the parent's in the buffer.
	path := parent[depth:]
	copy(path, parent[:depth])
	extendPath(path, depth, parentZero, parentOne, parentFeature)

	if node < 0 {
		leaf := t.LeafValue[^node]
		for i := 1; i <= depth; i++ {
			w := unwoundPathSum(path, depth, i)
			el := path[i]
			phi[el.feature] += w * (el.oneFraction - el.zeroFraction) * leaf
		}
		return
	}

	feature := t.SplitFeature[node]
	hot := t.next(node, x[feature])
	cold := t.LeftChild[node]
	if hot == cold {
		cold = t.RightChild[node]
	}
	w := t.count(node)
	hotZero, coldZero := t.count(hot)/w, t.count(cold)/w
	inZero, inOne := 1.0, 1.0

	// If this feature was split on higher up, undo that split and redo it here.
	pi := 0
	for ; pi <= depth; pi++ {
		if path[pi].feature == feature {
			break
		}
	}
	if pi != depth+1 {
		inZero, inOne = path[pi].zeroFraction, path[pi].oneFraction
		unwindPath(path, depth, pi)
		depth--
	}
	t.shap(x, phi, hot, depth+1, path, hotZero*inZero, inOne, feature)
	t.shap(x, phi, cold, depth+1, path, coldZero*inZero, 0, feature)
}

func extendPath(path []pathElement, depth int, zero, one float64, feature int) {
	path[depth] = pathElement{feature: feature, zeroFraction: zero, oneFraction: one}
	if depth == 0 {
		path[depth].pweight = 1
	}
	for i := depth - 1; i >= 0; i-- {
		path[i+1].pweight += one * path[i].pweight * float64(i+1) / float64(depth+1)
		path[i].pweight = zero * path[i].pweight * float64(depth-i) / float64(depth+1)
	}
}

func unwindPath(path []pathElement, depth, index int) {
	one, zero := path[index].oneFraction, path[index].zeroFraction
	next := path[depth].pweight
	for i := depth - 1; i >= 0; i-- {
		if one != 0 {
			tmp := path[i].pweight
			path[i].pweight = next * float64(depth+1) / (float64(i+1) * one)
			next = tmp - path[i].pweight*zero*float64(depth-i)/float64(depth+1)
		} else {
			path[i].pweight = path[i].pweight * float64(depth+1) / (zero * float64(depth-i))
		}
	}
	for i := index; i < depth; i++ {
		path[i].feature = path[i+1].feature
		path[i].zeroFraction = path[i+1].zeroFraction
		path[i].oneFraction = path[i+1].oneFraction
	}
}

func unwoundPathSum(path []pathElement, depth, index int) float64 {
	one, zero := path[index].oneFraction, path[index].zeroFraction
	next := path[depth].pweight
	var total float64
	for i := depth - 1; i >= 0; i-- {
		if one != 0 {
			tmp := next * float64(depth+1) / (float64(i+1) * one)
			total += tmp
			next = path[i].pweight - tmp*zero*float64(depth-i)/float64(depth+1)
		} else {
			total += path[i].pweight / zero / (float64(depth-i) / float64(depth+1))
		}
	}
	return total
}
