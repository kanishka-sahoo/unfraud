package profile

import "fmt"

// Describe explains a real-time feature column in plain language, for Case
// Packets. ok is false for columns that are not real-time features.
func (p Profile) Describe(column string) (string, bool) {
	for _, f := range p.Features {
		for i, c := range f.Columns() {
			if c != column {
				continue
			}
			w := ""
			if f.Kind.Windowed() {
				w = " in the previous " + WindowLabel(f.Windows[i])
			}
			switch f.Kind {
			case Count:
				return fmt.Sprintf("Events by this %s%s", f.Entity, w), true
			case Sum:
				return fmt.Sprintf("total %s by this %s%s", f.Field, f.Entity, w), true
			case RatioToMean:
				return fmt.Sprintf("this Event's %s divided by the %s's average%s", f.Field, f.Entity, w), true
			case Distinct:
				return fmt.Sprintf("distinct %s values seen with this %s%s", f.Of, f.Entity, w), true
			case NewPair:
				return fmt.Sprintf("1 if this %s was never seen with this %s before", f.Of, f.Entity), true
			case SinceLast:
				return fmt.Sprintf("seconds since this %s's previous Event", f.Entity), true
			case Changed:
				return fmt.Sprintf("1 if %s differs from this %s's previous value", f.Field, f.Entity), true
			case Delta:
				return fmt.Sprintf("change in %s since this %s's previous Event", f.Field, f.Entity), true
			case FraudLabels:
				return fmt.Sprintf("confirmed fraud labels on this %s's Events, released%s", f.Entity, w), true
			}
		}
	}
	return "", false
}
