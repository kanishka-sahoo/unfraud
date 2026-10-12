package profile

import (
	"hash/fnv"
	"math"
)

// Schema is the ordered list of feature names a Profile's Events carry.
// Events send values positionally, so producers and consumers must share it.
type Schema struct {
	Num, Cat []string
	// ID is a hash of the names and their order.
	ID uint32

	numIdx, catIdx map[string]int
}

// NewSchema builds a Schema. Names must be unique.
func NewSchema(num, cat []string) *Schema {
	h := fnv.New32a()
	s := &Schema{Num: num, Cat: cat, numIdx: make(map[string]int, len(num)), catIdx: make(map[string]int, len(cat))}
	for i, n := range num {
		s.numIdx[n] = i
		h.Write([]byte("n:" + n + "\x00"))
	}
	for i, n := range cat {
		s.catIdx[n] = i
		h.Write([]byte("c:" + n + "\x00"))
	}
	if len(s.numIdx) != len(num) || len(s.catIdx) != len(cat) {
		panic("profile: duplicate feature name in schema")
	}
	s.ID = h.Sum32()
	return s
}

// NumIndex returns the position of a numeric feature.
func (s *Schema) NumIndex(name string) (int, bool) { i, ok := s.numIdx[name]; return i, ok }

// CatIndex returns the position of a categorical feature.
func (s *Schema) CatIndex(name string) (int, bool) { i, ok := s.catIdx[name]; return i, ok }

// NewNum returns a numeric vector with every feature missing.
func (s *Schema) NewNum() []float64 {
	v := make([]float64, len(s.Num))
	for i := range v {
		v[i] = math.NaN()
	}
	return v
}

// NewCat returns a categorical vector with every feature missing.
func (s *Schema) NewCat() []string { return make([]string, len(s.Cat)) }
