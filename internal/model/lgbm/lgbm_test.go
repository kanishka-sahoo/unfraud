package lgbm

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Fixtures come from `unfraud-train fixture`: LightGBM models trained on
// synthetic data with a categorical feature, NaNs and exact zeros, plus
// LightGBM's own raw scores for probe inputs that hit every routing edge.

func readCSV(t *testing.T, path string) [][]float64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows [][]float64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var row []float64
		for _, s := range strings.Split(sc.Text(), ",") {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				t.Fatal(err)
			}
			row = append(row, v)
		}
		rows = append(rows, row)
	}
	return rows
}

func TestMatchesLightGBM(t *testing.T) {
	inputs := readCSV(t, "testdata/inputs.csv")
	for _, name := range []string{"default", "zero_as_missing"} {
		t.Run(name, func(t *testing.T) {
			m, err := Load("testdata/" + name + ".txt")
			if err != nil {
				t.Fatal(err)
			}
			want := readCSV(t, "testdata/"+name+".expected.csv")
			if len(want) != len(inputs) {
				t.Fatalf("%d expected scores for %d inputs", len(want), len(inputs))
			}
			for i, x := range inputs {
				if got := m.Raw(x); math.Abs(got-want[i][0]) > 1e-12 {
					t.Errorf("input %d %v: raw %v, LightGBM %v", i, x, got, want[i][0])
				}
			}
		})
	}
}

func TestRejectsUnsupportedModels(t *testing.T) {
	for name, model := range map[string]string{
		"multiclass": "version=v4\nnum_class=3\nobjective=multiclass num_class:3\nfeature_names=a\n\nend of trees\n",
		"regression": "version=v4\nnum_class=1\nobjective=regression\nfeature_names=a\n\nend of trees\n",
		"old":        "version=v2\nnum_class=1\nobjective=binary sigmoid:1\nfeature_names=a\n\nend of trees\n",
		"truncated":  "version=v4\nnum_class=1\nobjective=binary sigmoid:1\n",
	} {
		if _, err := Parse(strings.NewReader(model)); err == nil {
			t.Errorf("%s model accepted", name)
		}
	}
}
