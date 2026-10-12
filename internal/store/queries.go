package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a Case does not exist.
var ErrNotFound = errors.New("not found")

// Overview is the dashboard's summary of one Profile.
type Overview struct {
	Profile   string           `json:"profile"`
	Decisions []DecisionCount  `json:"decisions"`
	Cases     CaseCounts       `json:"cases"`
	PerMinute []MinuteCount    `json:"per_minute"` // each of the last 60 minutes, by scoring time
	Recent    []CaseListItem   `json:"recent_cases"`
	Summaries map[string]int64 `json:"summaries"`
}

type DecisionCount struct {
	Stage  int16 `json:"stage"`
	Action int16 `json:"action"`
	Count  int64 `json:"count"`
}

type CaseCounts struct {
	Open      int64 `json:"open"`
	OpenBlock int64 `json:"open_block"`
	Confirmed int64 `json:"confirmed"`
	Dismissed int64 `json:"dismissed"`
	Escalated int64 `json:"escalated"`
}

type MinuteCount struct {
	Minute      time.Time `json:"minute"`
	Provisional int64     `json:"provisional"`
	Revised     int64     `json:"revised"`
	Flagged     int64     `json:"flagged"`
}

// CaseListItem is a Case without its heavy fields.
type CaseListItem struct {
	Profile       string            `json:"profile"`
	EventID       string            `json:"event_id"`
	EventTime     time.Time         `json:"event_time"`
	Entities      map[string]string `json:"entities"`
	Action        int16             `json:"action"`
	Probability   float32           `json:"probability"`
	FlaggedBy     int16             `json:"flagged_by"`
	ReasonCodes   []string          `json:"reason_codes"`
	Disposition   *string           `json:"disposition"`
	SummaryStatus string            `json:"summary_status"`
	CreatedAt     time.Time         `json:"created_at"`
}

const listColumns = `profile, event_id, event_time, entities, action, probability, flagged_by, reason_codes,
	disposition, summary_status, created_at`

func (s *Store) Overview(ctx context.Context, profile string) (Overview, error) {
	o := Overview{Profile: profile, Summaries: map[string]int64{}}
	rows, err := s.Pool.Query(ctx, `SELECT stage, action, count(*) FROM decisions WHERE profile = $1 GROUP BY 1, 2 ORDER BY 1, 2`, profile)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var d DecisionCount
		if err := rows.Scan(&d.Stage, &d.Action, &d.Count); err != nil {
			return o, err
		}
		o.Decisions = append(o.Decisions, d)
	}
	rows.Close()
	err = s.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE disposition IS NULL),
		       count(*) FILTER (WHERE disposition IS NULL AND action = 2),
		       count(*) FILTER (WHERE disposition = 'confirmed'),
		       count(*) FILTER (WHERE disposition = 'dismissed'),
		       count(*) FILTER (WHERE disposition = 'escalated')
		FROM cases WHERE profile = $1`, profile).
		Scan(&o.Cases.Open, &o.Cases.OpenBlock, &o.Cases.Confirmed, &o.Cases.Dismissed, &o.Cases.Escalated)
	if err != nil {
		return o, err
	}
	rows, err = s.Pool.Query(ctx, `
		SELECT m.minute, count(*) FILTER (WHERE d.stage = 1), count(*) FILTER (WHERE d.stage = 2),
		       count(*) FILTER (WHERE d.action > 0)
		FROM generate_series(date_trunc('minute', now()) - interval '59 minutes', date_trunc('minute', now()),
		                     interval '1 minute') AS m(minute)
		LEFT JOIN decisions d ON d.profile = $1 AND d.scored_at >= m.minute AND d.scored_at < m.minute + interval '1 minute'
		GROUP BY m.minute ORDER BY m.minute`, profile)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var m MinuteCount
		if err := rows.Scan(&m.Minute, &m.Provisional, &m.Revised, &m.Flagged); err != nil {
			return o, err
		}
		o.PerMinute = append(o.PerMinute, m)
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT summary_status, count(*) FROM cases WHERE profile = $1 GROUP BY 1`, profile)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return o, err
		}
		o.Summaries[k] = n
	}
	rows.Close()
	o.Recent, err = s.ListCases(ctx, CaseFilter{Profile: profile, Status: "open", Sort: "recent", Limit: 8})
	return o, err
}

// CaseFilter selects Cases for the queue.
type CaseFilter struct {
	Profile   string
	Status    string // open, disposed, all
	MinAction int16
	Sort      string // score, time, recent
	Limit     int
	Offset    int
	Entity    string // type=id, optional
}

func (s *Store) ListCases(ctx context.Context, f CaseFilter) ([]CaseListItem, error) {
	where := `profile = $1 AND action >= $2`
	switch f.Status {
	case "open", "":
		where += ` AND disposition IS NULL`
	case "disposed":
		where += ` AND disposition IS NOT NULL`
	case "all":
	default:
		return nil, fmt.Errorf("unknown status %q", f.Status)
	}
	args := []any{f.Profile, f.MinAction}
	if f.Entity != "" {
		typ, id, ok := strings.Cut(f.Entity, "=")
		if !ok {
			return nil, fmt.Errorf("entity filter %q is not type=id", f.Entity)
		}
		where += ` AND entities ->> $3 = $4`
		args = append(args, typ, id)
	}
	order := map[string]string{
		"score":  "action DESC, probability DESC, event_id",
		"time":   "event_time DESC, event_id",
		"recent": "created_at DESC, event_id",
		"":       "action DESC, probability DESC, event_id",
	}[f.Sort]
	if order == "" {
		return nil, fmt.Errorf("unknown sort %q", f.Sort)
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+listColumns+` FROM cases WHERE `+where+
		` ORDER BY `+order+fmt.Sprintf(` LIMIT %d OFFSET %d`, f.Limit, max(f.Offset, 0)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CaseListItem{}
	for rows.Next() {
		var c CaseListItem
		if err := rows.Scan(&c.Profile, &c.EventID, &c.EventTime, &c.Entities, &c.Action, &c.Probability, &c.FlaggedBy,
			&c.ReasonCodes, &c.Disposition, &c.SummaryStatus, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// EventDecisions returns an Event's Decisions, Provisional first.
func (s *Store) EventDecisions(ctx context.Context, k CaseKey) ([]Decision, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT stage, event_time, probability, model_action, rule_action, action, reason_codes, model_version,
		       scored_at, ingested_at
		FROM decisions WHERE profile = $1 AND event_id = $2 ORDER BY stage`, k.Profile, k.EventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Decision{}
	for rows.Next() {
		d := Decision{Profile: k.Profile, EventID: k.EventID}
		if err := rows.Scan(&d.Stage, &d.EventTime, &d.Probability, &d.ModelAction, &d.RuleAction, &d.Action,
			&d.ReasonCodes, &d.ModelVersion, &d.ScoredAt, &d.IngestedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetDisposition records an analyst's judgment. Dispositions are never
// Labels and never feed metrics or training (ADR-0011).
func (s *Store) SetDisposition(ctx context.Context, k CaseKey, disposition, note string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE cases SET disposition = $3, disposition_note = NULLIF($4, ''),
		disposed_at = now(), updated_at = now() WHERE profile = $1 AND event_id = $2`, k.Profile, k.EventID, disposition, note)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ModelUse is one model version's share of Decisions.
type ModelUse struct {
	Profile      string    `json:"profile"`
	Stage        int16     `json:"stage"`
	ModelVersion string    `json:"model_version"`
	Decisions    int64     `json:"decisions"`
	Flagged      int64     `json:"flagged"`
	FirstScored  time.Time `json:"first_scored"`
	LastScored   time.Time `json:"last_scored"`
}

func (s *Store) ModelUses(ctx context.Context) ([]ModelUse, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT profile, stage, model_version, count(*), count(*) FILTER (WHERE action > 0), min(scored_at), max(scored_at)
		FROM decisions GROUP BY 1, 2, 3 ORDER BY 1, 2, 7 DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelUse{}
	for rows.Next() {
		var m ModelUse
		if err := rows.Scan(&m.Profile, &m.Stage, &m.ModelVersion, &m.Decisions, &m.Flagged, &m.FirstScored, &m.LastScored); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Profiles lists Profiles that have Decisions.
func (s *Store) Profiles(ctx context.Context) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT profile FROM cases ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
