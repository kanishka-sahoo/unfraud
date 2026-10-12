package rules

import (
	"math"
	"slices"
	"testing"

	"github.com/kanishka-sahoo/unfraud/internal/features"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
)

var schema = profile.NewSchema([]string{"amt"}, []string{"country"})

func TestRulesFireOnlyWhenAllConditionsHold(t *testing.T) {
	s, err := Compile([]profile.Rule{
		{Code: "BIG_FOREIGN", Action: profile.Block, When: []profile.Condition{
			{Feature: "amt", Op: profile.Ge, Value: 100},
			{Feature: "country", Op: profile.Ne, Str: "US"},
		}},
		{Code: "BURST", Action: profile.Review, When: []profile.Condition{{Feature: "count_1h", Op: profile.Gt, Value: 3}}},
	}, schema, []string{"count_1h"})
	if err != nil {
		t.Fatal(err)
	}
	nan := math.NaN()
	for _, tc := range []struct {
		amt     float64
		country string
		count   float64
		want    []string
	}{
		{150, "FR", 0, []string{"BIG_FOREIGN"}},
		{150, "US", 4, []string{"BURST"}},
		{99, "FR", 3, nil},
		{150, "", 0, nil},     // missing categorical never matches, even with !=
		{nan, "FR", nan, nil}, // missing numbers never match
		{100, "FR", 9, []string{"BIG_FOREIGN", "BURST"}},
	} {
		var got []string
		for _, h := range s.Evaluate([]float64{tc.amt}, []string{tc.country}, []float64{tc.count}) {
			got = append(got, h.Code)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("amt=%v country=%q count=%v: fired %v, want %v", tc.amt, tc.country, tc.count, got, tc.want)
		}
	}
}

func TestCompileRejectsBadRules(t *testing.T) {
	for name, r := range map[string]profile.Rule{
		"unknown feature": {Code: "X", When: []profile.Condition{{Feature: "nope", Op: profile.Gt}}},
		"cat ordering":    {Code: "X", When: []profile.Condition{{Feature: "country", Op: profile.Gt}}},
		"bad op":          {Code: "X", When: []profile.Condition{{Feature: "amt", Op: "~"}}},
		"no conditions":   {Code: "X"},
		"no code":         {When: []profile.Condition{{Feature: "amt", Op: profile.Gt}}},
	} {
		if _, err := Compile([]profile.Rule{r}, schema, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestProfileRulesCompile(t *testing.T) {
	for _, name := range registry.Names() {
		p, _ := registry.Get(name)
		eng, err := features.New(p, features.NewMemory())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Compile(p.Rules, p.Schema, eng.Columns()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRetiredRulesNeverFire(t *testing.T) {
	s, err := Compile([]profile.Rule{
		{Code: "OLD", Retired: "no fraud caught", Action: profile.Block, When: []profile.Condition{{Feature: "amt", Op: profile.Ge, Value: 0}}},
	}, schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hits := s.Evaluate([]float64{5}, []string{"US"}, nil); len(hits) != 0 {
		t.Fatalf("retired rule fired: %v", hits)
	}
}
