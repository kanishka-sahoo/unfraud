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
	// Calibration maps the trees' raw score to calibrated log-odds:
	// slope * raw + intercept (Platt scaling, fitted on the validation
	// period). Models without one use the raw score.
	Calibration *struct {
		Slope     float64 `json:"slope"`
		Intercept float64 `json:"intercept"`
	} `json:"calibration"`
}

// LogOdds turns the trees' raw score into calibrated log-odds.
func (m *Manifest) LogOdds(raw float64) float64 {
	if m.Calibration == nil {
		return raw
	}
	return m.Calibration.Slope*raw + m.Calibration.Intercept
}

// Score returns x's calibrated log-odds and fraud probability.
func (m *Model) Score(x []float64) (logOdds, probability float64) {
	logOdds = m.LogOdds(m.Trees.Raw(x))
	return logOdds, 1 / (1 + math.Exp(-logOdds))
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

// LoadArtifact reads a model folder (model.txt and manifest.json) without
// binding its inputs, for consumers that explain rather than score.
func LoadArtifact(dir string) (*Model, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m Model
	if err := json.Unmarshal(b, &m.Manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	if m.Trees, err = lgbm.Load(filepath.Join(dir, "model.txt")); err != nil {
		return nil, err
	}
	if len(m.Trees.FeatureNames) != len(m.Features) {
		return nil, fmt.Errorf("model %s: %d tree features, manifest lists %d", dir, len(m.Trees.FeatureNames), len(m.Features))
	}
	return &m, nil
}

// Load reads a model folder and binds its inputs to a Profile's Feature
// Schema and real-time feature columns.
func Load(dir string, schema *profile.Schema, realtime []string) (*Model, error) {
	mp, err := LoadArtifact(dir)
	if err != nil {
		return nil, err
	}
	m := *mp
	if m.SchemaID != schema.ID {
		return nil, fmt.Errorf("model %s was trained on schema %d, profile has %d", dir, m.SchemaID, schema.ID)
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

// Explain returns the Contributions behind x's score in calibrated
// log-odds: the bias, and per feature its SHAP value and its input value (the
// category name for categorical features, nil when missing). Calibration is
// linear, so they still add up to Score's log-odds.
func (m *Model) Explain(x []float64) (bias float64, shap []float64, values []any) {
	phi := make([]float64, len(m.Features)+1)
	m.Trees.Contributions(x, phi)
	if c := m.Calibration; c != nil {
		for i := range phi {
			phi[i] *= c.Slope
		}
		phi[len(m.Features)] += c.Intercept
	}
	cats := map[string]bool{}
	for _, c := range m.Categorical {
		cats[c] = true
	}
	values = make([]any, len(m.Features))
	for i, f := range m.Features {
		switch {
		case math.IsNaN(x[i]):
		case cats[f]:
			if code := int(x[i]); code >= 0 && code < len(m.Vocab[f]) {
				values[i] = m.Vocab[f][code]
			}
		default:
			values[i] = x[i]
		}
	}
	return phi[len(m.Features)], phi[:len(m.Features)], values
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
