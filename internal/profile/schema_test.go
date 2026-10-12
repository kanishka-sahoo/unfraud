package profile

import (
	"math"
	"testing"
)

func TestSchemaIDDependsOnNamesAndOrder(t *testing.T) {
	a := NewSchema([]string{"x", "y"}, []string{"c"})
	if a.ID != NewSchema([]string{"x", "y"}, []string{"c"}).ID {
		t.Fatal("same schema, different IDs")
	}
	for _, other := range []*Schema{
		NewSchema([]string{"y", "x"}, []string{"c"}),
		NewSchema([]string{"x", "y", "c"}, nil),
		NewSchema([]string{"x"}, []string{"y", "c"}),
	} {
		if other.ID == a.ID {
			t.Fatalf("schemas %v/%v and %v/%v share an ID", a.Num, a.Cat, other.Num, other.Cat)
		}
	}
}

func TestNewVectorsStartMissing(t *testing.T) {
	s := NewSchema([]string{"x", "y"}, []string{"c"})
	for _, v := range s.NewNum() {
		if !math.IsNaN(v) {
			t.Fatal("numeric features must start as NaN")
		}
	}
	if c := s.NewCat(); len(c) != 1 || c[0] != "" {
		t.Fatal("categorical features must start empty")
	}
}
