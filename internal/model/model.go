// Package model loads trained model artifacts and turns Events into model
// inputs exactly as training did.
package model

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/kanishka-sahoo/unfraud/internal/model/lgbm"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
)

// Manifest is the training CLI's description of a model (model manifest.json).
type Manifest struct {
	Profile     string              `json:"profile"`
	FeatureSet  string              `json:"feature_set"`
	SchemaID    uint32              `json:"schema_id"`
	Features    []string            `json:"features"`
	Categorical []string            `json:"categorical"`
	Vocab       map[string][]string `json:"vocab"`
	// Budgets maps an Alert Budget ("0.005") to the probability threshold
	// that flags that share of the validation month.
	Budgets map[string]struct {
		Threshold float64 `json:"threshold"`
	} `json:"test_budgets"`
}

// Threshold returns the probability threshold for an Alert Budget.
func (m *Manifest) Threshold(budget string) (float64, error) {
	b, ok := m.Budgets[budget]
	if !ok {
		return 0, fmt.Errorf("model has no threshold for alert budget %s", budget)
	}
	return b.Threshold, nil
}

// Model is a loaded artifact: the trees plus the input encoding.
type Model struct {
	Manifest
	Trees *lgbm.Model
	enc   []source
}

// source says where one model input comes from.
type source struct {
	kind  int // srcNum, srcCat or srcRealtime
	idx   int
	vocab map[string]int
}

const (
	srcNum = iota
	srcCat
	srcRealtime
)

// Load reads a model folder (model.txt and manifest.json) and binds its
// inputs to a Profile's Feature Schema and real-time feature columns.
func Load(dir string, schema *profile.Schema, realtime []string) (*Model, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m Model
	if err := json.Unmarshal(b, &m.Manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	if m.SchemaID != schema.ID {
		return nil, fmt.Errorf("model %s was trained on schema %d, profile has %d", dir, m.SchemaID, schema.ID)
	}
	if m.Trees, err = lgbm.Load(filepath.Join(dir, "model.txt")); err != nil {
		return nil, err
	}
	if len(m.Trees.FeatureNames) != len(m.Features) {
		return nil, fmt.Errorf("model %s: %d tree features, manifest lists %d", dir, len(m.Trees.FeatureNames), len(m.Features))
	}
	rt := make(map[string]int, len(realtime))
	for i, n := range realtime {
		rt[n] = i
	}
	cats := map[string]bool{}
	for _, c := range m.Categorical {
		cats[c] = true
	}
	for _, f := range m.Features {
		switch {
		case cats[f]:
			i, ok := schema.CatIndex(f)
			if !ok {
				return nil, fmt.Errorf("model %s: categorical feature %q not in schema", dir, f)
			}
			vocab := make(map[string]int, len(m.Vocab[f]))
			for code, v := range m.Vocab[f] {
				vocab[v] = code
			}
			m.enc = append(m.enc, source{kind: srcCat, idx: i, vocab: vocab})
		default:
			if i, ok := schema.NumIndex(f); ok {
				m.enc = append(m.enc, source{kind: srcNum, idx: i})
			} else if i, ok := rt[f]; ok {
				m.enc = append(m.enc, source{kind: srcRealtime, idx: i})
			} else {
				return nil, fmt.Errorf("model %s: feature %q is neither in the schema nor real-time", dir, f)
			}
		}
	}
	return &m, nil
}

// Encode writes the model input for one Event into x (len(Features)).
// Values are rounded to float32 because training matrices are float32, so
// split thresholds compare against identical values.
func (m *Model) Encode(x []float64, num []float64, cat []string, realtime []float64) {
	for i, s := range m.enc {
		var v float64
		switch s.kind {
		case srcNum:
			v = num[s.idx]
		case srcRealtime:
			v = realtime[s.idx]
		case srcCat:
			code, ok := s.vocab[cat[s.idx]]
			if !ok {
				x[i] = math.NaN()
				continue
			}
			v = float64(code)
		}
		x[i] = float64(float32(v))
	}
}
