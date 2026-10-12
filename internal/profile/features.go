package profile

import (
	"fmt"
	"time"
)

// FeatureKind is the kind of real-time feature kept per Entity.
type FeatureKind int

const (
	// Count is the number of the Entity's Events in the window.
	Count FeatureKind = iota + 1
	// Sum totals a numeric Field over the Entity's Events in the window.
	Sum
	// RatioToMean divides the Event's Field by the Entity's mean Field in the window.
	RatioToMean
	// Distinct counts distinct Entities of type Of seen with the Entity in the window.
	Distinct
	// NewPair is 1 when the Event's Entity of type Of has never been seen with
	// this Entity (within retention), else 0.
	NewPair
	// SinceLast is seconds since the Entity's previous Event.
	SinceLast
	// Changed is 1 when Field differs from its value on the Entity's previous
	// Event that had it, else 0.
	Changed
	// Delta is the numeric Field minus its value on the Entity's previous
	// Event that had it.
	Delta
	// FraudLabels counts fraud Labels released in the window for Events of the Entity.
	FraudLabels
)

var kindNames = map[FeatureKind]string{
	Count: "count", Sum: "sum", RatioToMean: "ratio_to_mean", Distinct: "distinct",
	NewPair: "new_pair", SinceLast: "since_last", Changed: "changed", Delta: "delta",
	FraudLabels: "fraud_labels",
}

func (k FeatureKind) String() string { return kindNames[k] }

// Windowed reports whether the kind is evaluated over Windows.
func (k FeatureKind) Windowed() bool {
	switch k {
	case Count, Sum, RatioToMean, Distinct, FraudLabels:
		return true
	}
	return false
}

// Feature declares one real-time feature. All features describe the Entity's
// history before the Event being scored; the Event itself is never included.
type Feature struct {
	Name    string
	Kind    FeatureKind
	Entity  string
	Of      string // Distinct, NewPair
	Field   string // Sum, RatioToMean, Changed, Delta
	Windows []time.Duration
}

// Columns returns the output column names, one per window for windowed kinds.
func (f Feature) Columns() []string {
	if !f.Kind.Windowed() {
		return []string{f.Name}
	}
	cols := make([]string, len(f.Windows))
	for i, w := range f.Windows {
		cols[i] = f.Name + "_" + WindowLabel(w)
	}
	return cols
}

// WindowLabel renders a window compactly: 1h, 24h, 7d.
func WindowLabel(w time.Duration) string {
	if w >= 48*time.Hour && w%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", w/(24*time.Hour))
	}
	if w%time.Hour == 0 {
		return fmt.Sprintf("%dh", w/time.Hour)
	}
	return w.String()
}

// Validate checks a feature against its Profile's Schema.
func (f Feature) Validate(s *Schema) error {
	if f.Name == "" || f.Entity == "" {
		return fmt.Errorf("feature %q: name and entity are required", f.Name)
	}
	if f.Kind.Windowed() != (len(f.Windows) > 0) {
		return fmt.Errorf("feature %s: %s needs windows exactly when it is windowed", f.Name, f.Kind)
	}
	switch f.Kind {
	case Distinct, NewPair:
		if f.Of == "" {
			return fmt.Errorf("feature %s: %s needs Of", f.Name, f.Kind)
		}
	case Sum, RatioToMean, Delta:
		if _, ok := s.NumIndex(f.Field); !ok {
			return fmt.Errorf("feature %s: numeric field %q not in schema", f.Name, f.Field)
		}
	case Changed:
		_, num := s.NumIndex(f.Field)
		_, cat := s.CatIndex(f.Field)
		if !num && !cat {
			return fmt.Errorf("feature %s: field %q not in schema", f.Name, f.Field)
		}
	case Count, SinceLast, FraudLabels:
	default:
		return fmt.Errorf("feature %s: unknown kind %d", f.Name, f.Kind)
	}
	return nil
}
