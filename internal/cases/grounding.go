package cases

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	// idToken matches identifier-like tokens such as Card UIDs (13926_315_-13)
	// or device strings (Windows|chrome 63.0); they must appear verbatim.
	idToken = regexp.MustCompile(`[A-Za-z0-9.\-]*[_|][A-Za-z0-9_|.\-]+`)
	number  = regexp.MustCompile(`-?\d[\d,]*(?:\.\d+)?(?:[eE][+-]?\d+)?`)
)

// Ground checks that every number and identifier in text comes from the
// Packet. Numbers may be rounded versions of a Packet value, or percentages
// of one; small whole numbers (up to 10) are allowed for counting words.
// It returns what could not be matched.
func Ground(text string, pk Packet) []string {
	raw, err := json.Marshal(pk)
	if err != nil {
		return []string{err.Error()}
	}
	packetText := string(raw)
	allowed := packetNumbers(raw)

	var problems []string
	rest := idToken.ReplaceAllStringFunc(text, func(tok string) string {
		if strings.IndexFunc(tok, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0 && !strings.Contains(packetText, strings.Trim(tok, ".-")) {
			problems = append(problems, "identifier "+tok)
		}
		return " "
	})
	for _, m := range number.FindAllString(rest, -1) {
		s := strings.ReplaceAll(m, ",", "")
		x, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		if x == math.Trunc(x) && math.Abs(x) <= 10 {
			continue
		}
		if !matches(x, decimals(s), allowed) {
			problems = append(problems, "number "+m)
		}
	}
	return problems
}

// decimals is how many decimal places s was written with, counting an
// exponent (2.76e+06 is precise to 10^4).
func decimals(s string) int {
	mant, exp, _ := strings.Cut(strings.ToLower(s), "e")
	d := 0
	if i := strings.IndexByte(mant, '.'); i >= 0 {
		d = len(mant) - i - 1
	}
	if exp != "" {
		e, _ := strconv.Atoi(exp)
		d -= e
	}
	return d
}

// matches reports whether x, written with d decimals, is a rounding of some
// allowed value or of it as a percentage.
func matches(x float64, d int, allowed []float64) bool {
	tol := 0.5*math.Pow(10, -float64(d)) + 1e-9
	for _, v := range allowed {
		for _, c := range []float64{v, v * 100, -v, -v * 100} {
			if math.Abs(x-c) <= tol {
				return true
			}
		}
	}
	return false
}

// packetNumbers collects every number in the Packet: numeric values and
// numbers inside strings (dates, feature names such as card_count_24h).
func packetNumbers(raw []byte) []float64 {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var out []float64
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case float64:
			out = append(out, t)
		case string:
			for _, m := range number.FindAllString(t, -1) {
				if f, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", ""), 64); err == nil {
					out = append(out, f)
				}
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		case map[string]any:
			for k, x := range t {
				walk(k)
				walk(x)
			}
		}
	}
	walk(v)
	return out
}

// Summary is what a driver writes for a Case.
type Summary struct {
	Text      string   `json:"summary"`
	KeyPoints []string `json:"key_points"`
}

// All returns the summary text and key points together, for Grounding.
func (s Summary) All() string { return s.Text + "\n" + strings.Join(s.KeyPoints, "\n") }

// Validate rejects empty or oversized summaries.
func (s Summary) Validate() error {
	if strings.TrimSpace(s.Text) == "" {
		return fmt.Errorf("empty summary")
	}
	if len(s.Text) > 2000 || len(s.KeyPoints) > 6 {
		return fmt.Errorf("summary too long")
	}
	return nil
}
