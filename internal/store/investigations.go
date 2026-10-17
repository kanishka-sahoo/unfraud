package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Investigation is an analyst's conversation with the assistant (ADR-0022),
// opened from a Case, a card, or nothing.
type Investigation struct {
	ID      string  `json:"id"`
	Profile string  `json:"profile"`
	CaseID  *string `json:"case_id"`
	Card    *string `json:"card"`
	Title   string  `json:"title"`
	// Context is what the assistant was told about the page it was opened
	// on, sent ahead of the first question.
	Context   json.RawMessage `json:"-"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Message is one message of an Investigation, as the Messages API takes it.
type Message struct {
	Seq  int    `json:"seq"`
	Role string `json:"role"` // user, assistant
	// Kind is question, tool_results, reply or stopped.
	Kind string `json:"kind"`
	// Content is the message's content blocks, exactly as sent to the API.
	Content json.RawMessage `json:"content"`
	// Display is what the console shows for tool results: a sentence and a
	// table per lookup.
	Display json.RawMessage `json:"display,omitempty"`
	// Meta records the model, tokens and cost of a reply, and what in it
	// could not be checked.
	Meta         json.RawMessage `json:"meta,omitempty"`
	SupersededAt *time.Time      `json:"superseded_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

const investigationColumns = `id, profile, case_id, card, title, context, created_at, updated_at`

func scanInvestigation(row pgx.Row) (Investigation, error) {
	var i Investigation
	err := row.Scan(&i.ID, &i.Profile, &i.CaseID, &i.Card, &i.Title, &i.Context, &i.CreatedAt, &i.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, err
}

// CreateInvestigation stores a new Investigation and returns it with its id.
func (s *Store) CreateInvestigation(ctx context.Context, i Investigation) (Investigation, error) {
	return scanInvestigation(s.Pool.QueryRow(ctx, `
		INSERT INTO investigations (profile, case_id, card, context) VALUES ($1, $2, $3, $4)
		RETURNING `+investigationColumns, i.Profile, i.CaseID, i.Card, i.Context))
}

// GetInvestigation returns one Investigation.
func (s *Store) GetInvestigation(ctx context.Context, id string) (Investigation, error) {
	if !isUUID(id) {
		return Investigation{}, ErrNotFound
	}
	return scanInvestigation(s.Pool.QueryRow(ctx, `SELECT `+investigationColumns+` FROM investigations WHERE id = $1`, id))
}

// ListInvestigations returns a Profile's Investigations that have at least
// one question, newest first: those opened on one Case when caseID is set.
func (s *Store) ListInvestigations(ctx context.Context, profile, caseID string, limit int) ([]Investigation, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+investigationColumns+` FROM investigations i
		WHERE profile = $1 AND ($2 = '' OR case_id = $2)
		  AND EXISTS (SELECT 1 FROM investigation_messages m WHERE m.investigation_id = i.id)
		ORDER BY updated_at DESC LIMIT $3`, profile, caseID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Investigation{}
	for rows.Next() {
		i, err := scanInvestigation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Messages returns an Investigation's messages in order; superseded ones
// (set aside by a rewind) only when all is true.
func (s *Store) Messages(ctx context.Context, id string, all bool) ([]Message, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT seq, role, kind, content, display, meta, superseded_at, created_at FROM investigation_messages
		WHERE investigation_id = $1 AND ($2 OR superseded_at IS NULL) ORDER BY seq`, id, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var content string
		if err := rows.Scan(&m.Seq, &m.Role, &m.Kind, &content, &m.Display, &m.Meta, &m.SupersededAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Content = json.RawMessage(content)
		out = append(out, m)
	}
	return out, rows.Err()
}

// AppendMessages adds messages after the Investigation's last one and returns
// them with their sequence numbers. The first question names the
// Investigation.
func (s *Store) AppendMessages(ctx context.Context, id string, ms ...Message) ([]Message, error) {
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var next int
		// Locking the Investigation serializes appends to it.
		if err := tx.QueryRow(ctx, `SELECT 1 FROM investigations WHERE id = $1 FOR UPDATE`, id).Scan(&next); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(seq), 0) + 1 FROM investigation_messages WHERE investigation_id = $1`,
			id).Scan(&next); err != nil {
			return err
		}
		for i := range ms {
			ms[i].Seq = next + i
			if err := tx.QueryRow(ctx, `
				INSERT INTO investigation_messages (investigation_id, seq, role, kind, content, display, meta)
				VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING created_at`,
				id, ms[i].Seq, ms[i].Role, ms[i].Kind, string(ms[i].Content), nullJSON(ms[i].Display), nullJSON(ms[i].Meta),
			).Scan(&ms[i].CreatedAt); err != nil {
				return err
			}
		}
		title := ""
		for _, m := range ms {
			if m.Kind == "question" {
				title = questionText(m.Content)
				break
			}
		}
		_, err := tx.Exec(ctx, `UPDATE investigations SET updated_at = now(),
			title = CASE WHEN title = '' AND $2 <> '' THEN left($2, 120) ELSE title END WHERE id = $1`, id, title)
		return err
	})
	return ms, err
}

// Rewind sets aside the messages from seq on, so the conversation continues
// from just before them. They are kept for audit.
func (s *Store) Rewind(ctx context.Context, id string, seq int) error {
	_, err := s.Pool.Exec(ctx, `UPDATE investigation_messages SET superseded_at = now()
		WHERE investigation_id = $1 AND seq >= $2 AND superseded_at IS NULL`, id, seq)
	return err
}

// InvestigationTokens is every token an Investigation's replies used,
// rewound ones included: what it has cost so far.
func (s *Store) InvestigationTokens(ctx context.Context, id string) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx, `
		SELECT COALESCE(sum(COALESCE((meta ->> 'input_tokens')::bigint, 0) + COALESCE((meta ->> 'output_tokens')::bigint, 0)), 0)
		FROM investigation_messages WHERE investigation_id = $1 AND kind = 'reply'`, id).Scan(&n)
	return n, err
}

// questionText is the text of a question's content blocks.
func questionText(content json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	json.Unmarshal(content, &blocks)
	for _, b := range blocks {
		if b.Type == "text" {
			return b.Text
		}
	}
	return ""
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if r != '-' {
				return false
			}
		case !('0' <= r && r <= '9' || 'a' <= r && r <= 'f' || 'A' <= r && r <= 'F'):
			return false
		}
	}
	return true
}
