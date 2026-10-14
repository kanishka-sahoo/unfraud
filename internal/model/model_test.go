package model

import (
	"math"
	"testing"

	"github.com/kanishka-sahoo/unfraud/internal/model/lgbm"
)

func TestCalibratedContributionsAddUpToTheScore(t *testing.T) {
	trees, err := lgbm.Load("lgbm/testdata/default.txt")
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{Trees: trees}
	m.Features = trees.FeatureNames
	x := []float64{0.13, math.NaN(), 0.64, -1, math.NaN(), 0.36}
	raw := trees.Raw(x)
	for _, cal := range []struct{ slope, intercept float64 }{{1, 0}, {0.74, 2.85}} {
		m.Calibration = &struct {
			Slope     float64 `json:"slope"`
			Intercept float64 `json:"intercept"`
		}{cal.slope, cal.intercept}
		logOdds, p := m.Score(x)
		if want := cal.slope*raw + cal.intercept; math.Abs(logOdds-want) > 1e-12 {
			t.Fatalf("log-odds %v, want %v", logOdds, want)
		}
		if want := 1 / (1 + math.Exp(-logOdds)); p != want {
			t.Fatalf("probability %v, want %v", p, want)
		}
		bias, shap, _ := m.Explain(x)
		sum := bias
		for _, v := range shap {
			sum += v
		}
		if math.Abs(sum-logOdds) > 1e-9 {
			t.Fatalf("calibration %+v: contributions add up to %v, score is %v", cal, sum, logOdds)
		}
	}
}
