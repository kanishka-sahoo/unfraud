package store

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// Decision is one row of the decisions table.
type Decision struct {
	Profile, EventID string
	Stage            int16
	EventTime        time.Time
	Probability      float32
	ModelAction      int16
	RuleAction       int16
	Action           int16
	ReasonCodes      []string
	ModelVersion     string
	ScoredAt         time.Time
	IngestedAt       time.Time
	Entities         map[string]string
}

// InsertDecisions stores Decisions, ignoring ones already stored.
func (s *Store) InsertDecisions(ctx context.Context, ds []Decision) error {
	if len(ds) == 0 {
		return nil
	}
	n := len(ds)
	prof, ids, rcs, vers := make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	stage, ma, ra, act := make([]int16, n), make([]int16, n), make([]int16, n), make([]int16, n)
	et, sa, ia := make([]time.Time, n), make([]time.Time, n), make([]time.Time, n)
	prob := make([]float32, n)
	for i, d := range ds {
		prof[i], ids[i], stage[i], et[i], prob[i] = d.Profile, d.EventID, d.Stage, d.EventTime, d.Probability
		ma[i], ra[i], act[i] = d.ModelAction, d.RuleAction, d.Action
		rcs[i], vers[i], sa[i], ia[i] = strings.Join(d.ReasonCodes, ","), d.ModelVersion, d.ScoredAt, d.IngestedAt
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO decisions (profile, event_id, stage, event_time, probability, model_action, rule_action,
		                       action, reason_codes, model_version, scored_at, ingested_at)
		SELECT p, e, st, et, pr, ma, ra, a, COALESCE(string_to_array(NULLIF(rc, ''), ','), '{}'), v, sa, ia
		FROM unnest($1::text[], $2::text[], $3::smallint[], $4::timestamptz[], $5::real[], $6::smallint[],
		            $7::smallint[], $8::smallint[], $9::text[], $10::text[], $11::timestamptz[], $12::timestamptz[])
		     AS t(p, e, st, et, pr, ma, ra, a, rc, v, sa, ia)
		ON CONFLICT DO NOTHING`,
		prof, ids, stage, et, prob, ma, ra, act, rcs, vers, sa, ia)
	return err
}

// CaseKey identifies a Case.
type CaseKey struct{ Profile, EventID string }

// UpsertCases opens Cases for flagged Decisions or makes existing ones
// stricter; an action is never lowered. It returns the Cases whose
// Contributions must be (re)computed: new ones, and ones whose action rose.
func (s *Store) UpsertCases(ctx context.Context, ds []Decision) ([]CaseKey, error) {
	// One row per Case per statement: Postgres can't update a row twice.
	strictest := map[CaseKey]Decision{}
	var order []CaseKey
	for _, d := range ds {
		if d.Action == 0 {
			continue
		}
		k := CaseKey{d.Profile, d.EventID}
		prev, seen := strictest[k]
		switch {
		case !seen:
			order = append(order, k)
			strictest[k] = d
		case d.Action > prev.Action:
			d.ReasonCodes = append(slices.Clone(d.ReasonCodes), prev.ReasonCodes...)
			strictest[k] = d
		default:
			prev.ReasonCodes = append(slices.Clone(prev.ReasonCodes), d.ReasonCodes...)
			strictest[k] = prev
		}
	}
	if len(order) == 0 {
		return nil, nil
	}
	n := len(order)
	prof, ids, ents, rcs, vers := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	act, stage := make([]int16, n), make([]int16, n)
	et := make([]time.Time, n)
	prob := make([]float32, n)
	for i, k := range order {
		d := strictest[k]
		e, err := json.Marshal(d.Entities)
		if err != nil {
			return nil, err
		}
		prof[i], ids[i], ents[i], rcs[i], vers[i] = d.Profile, d.EventID, string(e), strings.Join(d.ReasonCodes, ","), d.ModelVersion
		act[i], stage[i], et[i], prob[i] = d.Action, d.Stage, d.EventTime, d.Probability
	}
	rows, err := s.Pool.Query(ctx, `
		INSERT INTO cases AS c (profile, event_id, event_time, entities, action, probability, flagged_by,
		                        reason_codes, model_version)
		SELECT p, e, et, en::jsonb, a, pr, st,
		       ARRAY(SELECT DISTINCT x FROM unnest(string_to_array(NULLIF(rc, ''), ',')) x ORDER BY x), v
		FROM unnest($1::text[], $2::text[], $3::timestamptz[], $4::text[], $5::smallint[], $6::real[],
		            $7::smallint[], $8::text[], $9::text[]) AS t(p, e, et, en, a, pr, st, rc, v)
		ON CONFLICT (profile, event_id) DO UPDATE SET
		    reason_codes  = ARRAY(SELECT DISTINCT x FROM unnest(c.reason_codes || EXCLUDED.reason_codes) x ORDER BY x),
		    probability   = CASE WHEN EXCLUDED.action > c.action THEN EXCLUDED.probability ELSE c.probability END,
		    model_version = CASE WHEN EXCLUDED.action > c.action THEN EXCLUDED.model_version ELSE c.model_version END,
		    contributions = CASE WHEN EXCLUDED.action > c.action THEN NULL ELSE c.contributions END,
		    action        = GREATEST(c.action, EXCLUDED.action),
		    updated_at    = now()
		RETURNING profile, event_id, contributions IS NULL`,
		prof, ids, et, ents, act, prob, stage, rcs, vers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var need []CaseKey
	for rows.Next() {
		var k CaseKey
		var missing bool
		if err := rows.Scan(&k.Profile, &k.EventID, &missing); err != nil {
			return nil, err
		}
		if missing {
			need = append(need, k)
		}
	}
	return need, rows.Err()
}

// Contribution is one feature's share of a model score, for Case Packets.
type Contribution struct {
	Feature string  `json:"feature"`
	Value   any     `json:"value"` // number, category, or null when missing
	SHAP    float64 `json:"shap"`
}

// SetContributions stores a Case's Contributions and the score they explain.
func (s *Store) SetContributions(ctx context.Context, k CaseKey, c CaseContributions) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE cases SET contributions = $3, updated_at = now() WHERE profile = $1 AND event_id = $2`,
		k.Profile, k.EventID, b)
	return err
}

// CaseContributions explains the flagging model's score: the bias plus the
// listed features (the largest by magnitude) plus Other.
type CaseContributions struct {
	ModelVersion string         `json:"model_version"`
	Stage        int16          `json:"stage"`
	Bias         float64        `json:"bias"`
	Top          []Contribution `json:"top"`
	Other        float64        `json:"other"` // sum of the rest
	RawScore     float64        `json:"raw_score"`
}
