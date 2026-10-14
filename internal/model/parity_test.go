package model

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/kanishka-sahoo/unfraud/internal/profile/registry"
	"github.com/kanishka-sahoo/unfraud/internal/table"
)

// TestParityWithPython scores a trained model's test month in Go, starting
// from the raw features table, and compares with Python LightGBM's scores:
//
//	UNFRAUD_PARITY_MODEL=models/tabformer/lgbm-realtime \
//	UNFRAUD_PARITY_FEATURES=data/features/tabformer go test ./internal/model/
func TestParityWithPython(t *testing.T) {
	modelDir, featDir := os.Getenv("UNFRAUD_PARITY_MODEL"), os.Getenv("UNFRAUD_PARITY_FEATURES")
	if modelDir == "" || featDir == "" {
		t.Skip("UNFRAUD_PARITY_MODEL and UNFRAUD_PARITY_FEATURES not set")
	}
	root := "../.."
	var fm struct {
		Profile  string   `json:"profile"`
		Realtime []string `json:"realtime"`
	}
	b, err := os.ReadFile(filepath.Join(root, featDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &fm); err != nil {
		t.Fatal(err)
	}
	p, err := registry.Get(fm.Profile)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(filepath.Join(root, modelDir), p.Schema, fm.Realtime)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]float64{}
	pr, err := table.Open(filepath.Join(root, modelDir, "test_predictions.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	idCol, _ := pr.Index("event_id")
	scoreCol, _ := pr.Index("raw_score")
	for {
		row, err := pr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		want[string(row[idCol].ByteArray())] = row[scoreCol].Double()
	}
	pr.Close()

	fr, err := table.Open(filepath.Join(root, featDir, "features.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	defer fr.Close()
	col := func(name string) int {
		i, ok := fr.Index(name)
		if !ok {
			t.Fatalf("features table has no column %q", name)
		}
		return i
	}
	numCols := make([]int, len(p.Schema.Num))
	for i, n := range p.Schema.Num {
		numCols[i] = col(n)
	}
	catCols := make([]int, len(p.Schema.Cat))
	for i, n := range p.Schema.Cat {
		catCols[i] = col(n)
	}
	rtCols := make([]int, len(fm.Realtime))
	for i, n := range fm.Realtime {
		rtCols[i] = col(n)
	}
	idx := col("event_id")

	num, cat, rt := p.Schema.NewNum(), p.Schema.NewCat(), make([]float64, len(fm.Realtime))
	x := make([]float64, len(m.Features))
	floatOf := func(v interface {
		IsNull() bool
		Float() float32
	}) float64 {
		if v.IsNull() {
			return math.NaN()
		}
		return float64(v.Float())
	}
	var compared int
	var maxDiff float64
	for {
		row, err := fr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		w, ok := want[string(row[idx].ByteArray())]
		if !ok {
			continue
		}
		for i, c := range numCols {
			num[i] = floatOf(row[c])
		}
		for i, c := range catCols {
			cat[i] = ""
			if !row[c].IsNull() {
				cat[i] = string(row[c].ByteArray())
			}
		}
		for i, c := range rtCols {
			rt[i] = floatOf(row[c])
		}
		m.Encode(x, num, cat, rt)
		maxDiff = math.Max(maxDiff, math.Abs(m.Trees.Raw(x)-w))
		compared++
	}
	if compared != len(want) {
		t.Fatalf("compared %d of %d test predictions", compared, len(want))
	}
	t.Logf("compared %d Events, max |Go - Python| raw score = %.3g", compared, maxDiff)
	if maxDiff > 1e-6 {
		t.Fatalf("max raw-score difference %.3g exceeds 1e-6", maxDiff)
	}
}
