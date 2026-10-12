package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// Case is one row of the cases table.
type Case struct {
	Profile, EventID string
	EventTime        time.Time
	Entities         map[string]string
	Action           int16
	Probability      float32
	FlaggedBy        int16
	ReasonCodes      []string
	Contributions    *CaseContributions
	ModelVersion     string
	Disposition      *string
	DispositionNote  *string
	DisposedAt       *time.Time
	Summary          json.RawMessage
	SummaryStatus    string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const caseColumns = `profile, event_id, event_time, entities, action, probability, flagged_by, reason_codes,
	contributions, model_version, disposition, disposition_note, disposed_at, summary, summary_status,
	created_at, updated_at`

func scanCase(row pgx.Row) (Case, error) {
	var c Case
	var contrib []byte
	err := row.Scan(&c.Profile, &c.EventID, &c.EventTime, &c.Entities, &c.Action, &c.Probability, &c.FlaggedBy,
		&c.ReasonCodes, &contrib, &c.ModelVersion, &c.Disposition, &c.DispositionNote, &c.DisposedAt, &c.Summary,
		&c.SummaryStatus, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	if contrib != nil {
		c.Contributions = &CaseContributions{}
		if err := json.Unmarshal(contrib, c.Contributions); err != nil {
			return c, err
		}
	}
	return c, nil
}

// GetCase returns one Case.
func (s *Store) GetCase(ctx context.Context, k CaseKey) (Case, error) {
	return scanCase(s.Pool.QueryRow(ctx, `SELECT `+caseColumns+` FROM cases WHERE profile = $1 AND event_id = $2`,
		k.Profile, k.EventID))
}

// ClaimSummaries marks up to n pending Cases (with Contributions, at least
// minAction) as running and returns them, strictest and highest-scoring
// first. Concurrent workers never claim the same Case.
func (s *Store) ClaimSummaries(ctx context.Context, n int, minAction int16) ([]Case, error) {
	rows, err := s.Pool.Query(ctx, `
		UPDATE cases SET summary_status = 'running', summary_attempts = summary_attempts + 1, updated_at = now()
		WHERE (profile, event_id) IN (
		    SELECT profile, event_id FROM cases
		    WHERE summary_status = 'pending' AND contributions IS NOT NULL AND action >= $2
		    ORDER BY action DESC, probability DESC
		    LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING `+caseColumns, n, minAction)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Case
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveSummary stores a finished Summary (status done) or gives up on a Case
// (status failed), keeping what was learned in summary.
func (s *Store) SaveSummary(ctx context.Context, k CaseKey, status string, summary any) error {
	b, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE cases SET summary = $3, summary_status = $4, updated_at = now()
		WHERE profile = $1 AND event_id = $2`, k.Profile, k.EventID, b, status)
	return err
}

// RequeueSummary puts a Case back in the queue, for another attempt.
func (s *Store) RequeueSummary(ctx context.Context, k CaseKey) error {
	_, err := s.Pool.Exec(ctx, `UPDATE cases SET summary_status = 'pending', updated_at = now()
		WHERE profile = $1 AND event_id = $2`, k.Profile, k.EventID)
	return err
}

// ResetStaleSummaries requeues Cases a crashed worker left running.
func (s *Store) ResetStaleSummaries(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE cases SET summary_status = 'pending'
		WHERE summary_status = 'running' AND updated_at < now() - make_interval(secs => $1)`, olderThan.Seconds())
	return tag.RowsAffected(), err
}
