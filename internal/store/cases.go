package store

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// querier is a pool or a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// RecordDecisions stores Decisions and applies the new ones to Cases in one
// transaction, returning the new Decisions and the Cases whose Contributions
// must be (re)computed. Two transactions would let a crash between them keep
// a Decision whose Case was never updated: its redelivery is not new, so it
// would never be applied.
func (s *Store) RecordDecisions(ctx context.Context, ds []Decision) (fresh []Decision, need []CaseKey, err error) {
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if fresh, err = insertDecisions(ctx, tx, ds); err != nil {
			return err
		}
		need, err = upsertCases(ctx, tx, fresh)
		return err
	})
	return fresh, need, err
}

// Decision is one row of the decisions table.
type Decision struct {
	Profile      string            `json:"profile"`
	EventID      string            `json:"event_id"`
	Stage        int16             `json:"stage"`
	EventTime    time.Time         `json:"event_time"`
	Probability  float32           `json:"probability"`
	ModelAction  int16             `json:"model_action"`
	RuleAction   int16             `json:"rule_action"`
	Action       int16             `json:"action"`
	ReasonCodes  []string          `json:"reason_codes"`
	ModelVersion string            `json:"model_version"`
	ScoredAt     time.Time         `json:"scored_at"`
	IngestedAt   time.Time         `json:"ingested_at"`
	Entities     map[string]string `json:"entities,omitempty"`
}

// InsertDecisions stores Decisions, ignoring ones already stored, and
// returns the ones that were new. Only new Decisions may change Cases: a
// redelivered or replayed Event can score differently (its features already
// include its first pass), and the first Decision per Event and Stage stands.
func (s *Store) InsertDecisions(ctx context.Context, ds []Decision) ([]Decision, error) {
	return insertDecisions(ctx, s.Pool, ds)
}

func insertDecisions(ctx context.Context, q querier, ds []Decision) ([]Decision, error) {
	if len(ds) == 0 {
		return nil, nil
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
	rows, err := q.Query(ctx, `
		INSERT INTO decisions (profile, event_id, stage, event_time, probability, model_action, rule_action,
		                       action, reason_codes, model_version, scored_at, ingested_at)
		SELECT p, e, st, et, pr, ma, ra, a, COALESCE(string_to_array(NULLIF(rc, ''), ','), '{}'), v, sa, ia
		FROM unnest($1::text[], $2::text[], $3::smallint[], $4::timestamptz[], $5::real[], $6::smallint[],
		            $7::smallint[], $8::smallint[], $9::text[], $10::text[], $11::timestamptz[], $12::timestamptz[])
		     AS t(p, e, st, et, pr, ma, ra, a, rc, v, sa, ia)
		ON CONFLICT DO NOTHING
		RETURNING profile, event_id, stage`,
		prof, ids, stage, et, prob, ma, ra, act, rcs, vers, sa, ia)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct {
		p, e string
		s    int16
	}
	inserted := map[key]bool{}
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.p, &k.e, &k.s); err != nil {
			return nil, err
		}
		inserted[k] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var fresh []Decision
	for _, d := range ds {
		k := key{d.Profile, d.EventID, d.Stage}
		if inserted[k] {
			fresh = append(fresh, d)
			delete(inserted, k) // a batch may hold the same Decision twice
		}
	}
	return fresh, nil
}

// CaseKey identifies a Case.
type CaseKey struct{ Profile, EventID string }

// takeover is when a new Decision replaces the one a Case reflects: it is
// stricter, or equally strict from an earlier Stage (Provisional before
// Revised), whatever order the topics were consumed in.
const takeover = `(EXCLUDED.action > c.action OR (EXCLUDED.action = c.action AND EXCLUDED.flagged_by < c.flagged_by))`

// UpsertCases opens Cases for flagged Decisions or makes existing ones
// stricter; an action is never lowered. It returns the Cases whose
// Contributions must be (re)computed: new ones, and ones whose Decision was
// taken over.
func (s *Store) UpsertCases(ctx context.Context, ds []Decision) ([]CaseKey, error) {
	return upsertCases(ctx, s.Pool, ds)
}

func upsertCases(ctx context.Context, q querier, ds []Decision) ([]CaseKey, error) {
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
		case d.Action > prev.Action || d.Action == prev.Action && d.Stage < prev.Stage:
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
	rows, err := q.Query(ctx, `
		INSERT INTO cases AS c (profile, event_id, event_time, entities, action, probability, flagged_by,
		                        reason_codes, model_version)
		SELECT p, e, et, en::jsonb, a, pr, st,
		       ARRAY(SELECT DISTINCT x FROM unnest(string_to_array(NULLIF(rc, ''), ',')) x ORDER BY x), v
		FROM unnest($1::text[], $2::text[], $3::timestamptz[], $4::text[], $5::smallint[], $6::real[],
		            $7::smallint[], $8::text[], $9::text[]) AS t(p, e, et, en, a, pr, st, rc, v)
		ON CONFLICT (profile, event_id) DO UPDATE SET
		    reason_codes  = ARRAY(SELECT DISTINCT x FROM unnest(c.reason_codes || EXCLUDED.reason_codes) x ORDER BY x),
		    probability   = CASE WHEN `+takeover+` THEN EXCLUDED.probability ELSE c.probability END,
		    model_version = CASE WHEN `+takeover+` THEN EXCLUDED.model_version ELSE c.model_version END,
		    contributions = CASE WHEN `+takeover+` THEN NULL ELSE c.contributions END,
		    flagged_by    = CASE WHEN `+takeover+` THEN EXCLUDED.flagged_by ELSE c.flagged_by END,
		    -- A Summary describes one Decision; a takeover makes it stale.
		    summary        = CASE WHEN `+takeover+` THEN NULL ELSE c.summary END,
		    summary_status = CASE WHEN `+takeover+` THEN 'pending' ELSE c.summary_status END,
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
