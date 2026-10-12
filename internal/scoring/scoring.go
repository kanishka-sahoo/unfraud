// Package scoring turns input records into Decisions: real-time features,
// then the model, then Rules. The scorer service and offline Accuracy Runs
// share it, so live and offline Decisions come from the same code.
package scoring

import (
	"context"
	"fmt"
	"math"

	unfraudv1 "github.com/kanishka-sahoo/unfraud/gen/unfraud/v1"
	"github.com/kanishka-sahoo/unfraud/internal/features"
	"github.com/kanishka-sahoo/unfraud/internal/model"
	"github.com/kanishka-sahoo/unfraud/internal/profile"
	"github.com/kanishka-sahoo/unfraud/internal/rules"
)

// Config chooses the Alert Budgets behind the model's review and block
// thresholds. A stricter budget blocks; a looser one sends to review.
type Config struct {
	BlockBudget  string
	ReviewBudget string
}

var DefaultConfig = Config{BlockBudget: "0.005", ReviewBudget: "0.02"}

// Decision is the outcome for one Event.
type Decision struct {
	EventID     string
	EventTimeMs int64
	Entities    map[string]string
	Raw         float64 // model log-odds
	Probability float64
	ModelAction profile.Action
	RuleAction  profile.Action
	Action      profile.Action // the stricter of the two
	Hits        []rules.Hit
	// Input is the model input vector, kept so Contributions can be
	// computed later for flagged Events.
	Input []float64
}

// Scorer is not safe for concurrent use.
type Scorer struct {
	p             profile.Profile
	eng           *features.Engine
	m             *model.Model
	rules         *rules.Set
	block, review float64
}

func New(p profile.Profile, st features.State, modelDir string, cfg Config) (*Scorer, error) {
	eng, err := features.New(p, st)
	if err != nil {
		return nil, err
	}
	m, err := model.Load(modelDir, p.Schema, eng.Columns())
	if err != nil {
		return nil, err
	}
	rs, err := rules.Compile(p.Rules, p.Schema, eng.Columns())
	if err != nil {
		return nil, err
	}
	s := &Scorer{p: p, eng: eng, m: m, rules: rs}
	if s.block, err = m.Threshold(cfg.BlockBudget); err != nil {
		return nil, err
	}
	if s.review, err = m.Threshold(cfg.ReviewBudget); err != nil {
		return nil, err
	}
	if s.block < s.review {
		return nil, fmt.Errorf("block threshold %v is below review threshold %v", s.block, s.review)
	}
	return s, nil
}

// Model returns the loaded model.
func (s *Scorer) Model() *model.Model { return s.m }

// Process applies one input record and returns a Decision for Events, nil
// otherwise.
func (s *Scorer) Process(ctx context.Context, env *unfraudv1.Envelope) (*Decision, error) {
	rt, err := s.eng.Process(ctx, env)
	if err != nil {
		return nil, err
	}
	ev := env.GetEvent()
	if ev == nil {
		return nil, nil
	}
	if ev.SchemaId != s.p.Schema.ID {
		return nil, fmt.Errorf("event %s has schema %d, profile %s expects %d", ev.EventId, ev.SchemaId, s.p.Name, s.p.Schema.ID)
	}
	d := &Decision{EventID: ev.EventId, EventTimeMs: env.EventTimeMs, Entities: ev.Entities,
		Input: make([]float64, len(s.m.Features))}
	s.m.Encode(d.Input, ev.Num, ev.Cat, rt)
	d.Raw = s.m.Trees.Raw(d.Input)
	d.Probability = 1 / (1 + math.Exp(-d.Raw))
	switch {
	case d.Probability > s.block:
		d.ModelAction = profile.Block
	case d.Probability > s.review:
		d.ModelAction = profile.Review
	}
	d.Hits = s.rules.Evaluate(ev.Num, ev.Cat, rt)
	for _, h := range d.Hits {
		d.RuleAction = max(d.RuleAction, h.Action)
	}
	d.Action = max(d.ModelAction, d.RuleAction)
	return d, nil
}
