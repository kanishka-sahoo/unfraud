// Package rules evaluates a Profile's Rules against an Event's features.
package rules

import (
	"fmt"
	"math"

	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

// Set is a compiled list of Rules.
type Set struct {
	rules []compiled
}

type compiled struct {
	profile.Rule
	conds []cond
}

type cond struct {
	src   int // srcNum, srcCat or srcRealtime
	idx   int
	op    profile.Op
	value float64
	str   string
}

const (
	srcNum = iota
	srcCat
	srcRealtime
)

// Compile binds rules to a Profile's Feature Schema and real-time columns.
func Compile(rs []profile.Rule, schema *profile.Schema, realtime []string) (*Set, error) {
	rt := make(map[string]int, len(realtime))
	for i, n := range realtime {
		rt[n] = i
	}
	s := &Set{}
	codes := map[string]bool{}
	for _, r := range rs {
		if r.Retired != "" {
			continue
		}
		if r.Code == "" || codes[r.Code] {
			return nil, fmt.Errorf("rule %q: codes must be unique and non-empty", r.Code)
		}
		codes[r.Code] = true
		if len(r.When) == 0 {
			return nil, fmt.Errorf("rule %s: no conditions", r.Code)
		}
		c := compiled{Rule: r}
		for _, w := range r.When {
			k := cond{op: w.Op, value: w.Value, str: w.Str}
			switch {
			case schema.Has(w.Feature, false):
				k.src, k.idx = srcNum, mustIndex(schema.NumIndex(w.Feature))
			case schema.Has(w.Feature, true):
				if w.Op != profile.Eq && w.Op != profile.Ne {
					return nil, fmt.Errorf("rule %s: categorical %s only supports == and !=", r.Code, w.Feature)
				}
				k.src, k.idx = srcCat, mustIndex(schema.CatIndex(w.Feature))
			default:
				i, ok := rt[w.Feature]
				if !ok {
					return nil, fmt.Errorf("rule %s: unknown feature %q", r.Code, w.Feature)
				}
				k.src, k.idx = srcRealtime, i
			}
			switch w.Op {
			case profile.Gt, profile.Ge, profile.Lt, profile.Le, profile.Eq, profile.Ne:
			default:
				return nil, fmt.Errorf("rule %s: unknown operator %q", r.Code, w.Op)
			}
			c.conds = append(c.conds, k)
		}
		s.rules = append(s.rules, c)
	}
	return s, nil
}

func mustIndex(i int, _ bool) int { return i }

// Hit is a Rule that fired.
type Hit struct {
	Code   string
	Action profile.Action
}

// Evaluate returns the Rules that fire, in declaration order.
func (s *Set) Evaluate(num []float64, cat []string, realtime []float64) []Hit {
	var hits []Hit
	for _, r := range s.rules {
		if r.matches(num, cat, realtime) {
			hits = append(hits, Hit{Code: r.Code, Action: r.Action})
		}
	}
	return hits
}

func (r compiled) matches(num []float64, cat []string, realtime []float64) bool {
	for _, c := range r.conds {
		if c.src == srcCat {
			v := cat[c.idx]
			if v == "" || (c.op == profile.Eq) != (v == c.str) {
				return false
			}
			continue
		}
		v := num
		if c.src == srcRealtime {
			v = realtime
		}
		x := v[c.idx]
		if math.IsNaN(x) {
			return false
		}
		var ok bool
		switch c.op {
		case profile.Gt:
			ok = x > c.value
		case profile.Ge:
			ok = x >= c.value
		case profile.Lt:
			ok = x < c.value
		case profile.Le:
			ok = x <= c.value
		case profile.Eq:
			ok = x == c.value
		case profile.Ne:
			ok = x != c.value
		}
		if !ok {
			return false
		}
	}
	return true
}

// Codes lists the Reason Codes of all Rules, in order.
func (s *Set) Codes() []string {
	out := make([]string, len(s.rules))
	for i, r := range s.rules {
		out[i] = r.Code
	}
	return out
}
