package profile

import "testing"

func TestDuration(t *testing.T) {
	for secs, want := range map[float64]string{
		1: "1 second", 45: "45 seconds", 2340: "39 minutes", 2400: "40 minutes", 2370: "about 40 minutes",
		25200: "7 hours", 26000: "about 7 hours", 3 * 86400: "3 days",
	} {
		if got := Duration(secs); got != want {
			t.Errorf("Duration(%v) = %q, want %q", secs, got, want)
		}
	}
}
