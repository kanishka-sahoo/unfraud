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
			e := p.Noun(f.Entity)
			switch f.Kind {
			case Count:
				return fmt.Sprintf("payments by this %s%s", e, w), true
			case Sum:
				return fmt.Sprintf("total %s by this %s%s", p.Noun(f.Field), e, w), true
			case RatioToMean:
				return fmt.Sprintf("this payment's %s divided by the %s's average%s", p.Noun(f.Field), e, w), true
			case Distinct:
				return fmt.Sprintf("distinct %ss seen with this %s%s", p.Noun(f.Of), e, w), true
			case NewPair:
				return fmt.Sprintf("1 if this %s was never seen with this %s before", p.Noun(f.Of), e), true
			case SinceLast:
				return fmt.Sprintf("seconds since this %s's previous payment", e), true
			case Changed:
				return fmt.Sprintf("1 if the %s differs from this %s's previous payment", p.Noun(f.Field), e), true
			case Delta:
				return fmt.Sprintf("change in %s since this %s's previous payment", p.Noun(f.Field), e), true
			case FraudLabels:
				return fmt.Sprintf("confirmed fraud on this %s's payments, released%s", e, w), true
			}
		}
	}
	return "", false
}

// Noun is what analysts call an Entity type or field, or its name.
func (p Profile) Noun(entity string) string {
	if n, ok := p.Nouns[entity]; ok {
		return n
	}
	return entity
}

// Meaning spells out a coded category value, such as an MCC's merchant
// type; it is "" when the value needs no spelling out.
func (p Profile) Meaning(feature string, value any) string {
	s, ok := value.(string)
	if m := p.Meanings[feature][s]; ok && m != s {
		return m
	}
	return ""
}
