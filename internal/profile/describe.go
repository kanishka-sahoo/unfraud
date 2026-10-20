package profile

import (
	"fmt"
	"math"
)

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
// type, seconds as a duration ("39 minutes"), or that a negative amount is
// a refund; it is "" when the value needs no spelling out.
func (p Profile) Meaning(feature string, value any) string {
	if x, ok := value.(float64); ok {
		f, isFeature := p.feature(feature)
		switch {
		case isFeature && f.Kind == SinceLast:
			return Duration(x)
		case x < 0 && p.Amount != "" && feature == p.Amount:
			return "a refund: money returned to the card, not spent"
		case x < 0 && isFeature && f.Kind == RatioToMean && f.Field == p.Amount:
			return "negative because this payment is a refund"
		}
	}
	s, ok := value.(string)
	if m := p.Meanings[feature][s]; ok && m != s {
		return m
	}
	return ""
}

// feature is the Feature that computes a column.
func (p Profile) feature(column string) (Feature, bool) {
	for _, f := range p.Features {
		for _, c := range f.Columns() {
			if c == column {
				return f, true
			}
		}
	}
	return Feature{}, false
}

// Duration writes seconds as analysts say them, in the largest unit that
// keeps it readable: "45 seconds", "39 minutes", "about 7 hours", "3 days".
// Summaries quote it, so Grounding finds the number in the Case Packet.
func Duration(secs float64) string {
	for _, u := range []struct {
		name string
		size float64
		upTo float64
	}{{"second", 1, 90}, {"minute", 60, 90 * 60}, {"hour", 3600, 48 * 3600}, {"day", 86400, math.Inf(1)}} {
		if math.Abs(secs) >= u.upTo {
			continue
		}
		n := math.Round(secs / u.size)
		s := fmt.Sprintf("%.0f %s", n, u.name)
		if n != 1 {
			s += "s"
		}
		if n*u.size != secs {
			s = "about " + s
		}
		return s
	}
	return ""
}
