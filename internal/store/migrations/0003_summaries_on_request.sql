-- Summaries are written only when an analyst asks (ADR-0022). New Cases start
-- with no Summary; Cases still queued from before are let go unless someone
-- asked for them.
ALTER TABLE cases DROP CONSTRAINT cases_summary_status_check;
ALTER TABLE cases ADD CONSTRAINT cases_summary_status_check
    CHECK (summary_status IN ('none', 'pending', 'running', 'done', 'failed', 'skipped'));
ALTER TABLE cases ALTER COLUMN summary_status SET DEFAULT 'none';
UPDATE cases SET summary_status = 'none' WHERE summary_status = 'pending' AND NOT summary_requested;

-- An Investigation is an analyst's conversation with the assistant. Message
-- content is the Messages API's own JSON, kept as text because jsonb would
-- reorder it: thinking blocks must go back byte for byte. A rewind marks
-- later messages superseded instead of deleting them.
CREATE TABLE investigations (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    profile     text        NOT NULL,
    case_id     text,
    card        text,
    title       text        NOT NULL DEFAULT '',
    context     jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX investigations_case ON investigations (profile, case_id, updated_at DESC);
CREATE INDEX investigations_recent ON investigations (profile, updated_at DESC);

CREATE TABLE investigation_messages (
    investigation_id uuid        NOT NULL REFERENCES investigations ON DELETE CASCADE,
    seq              int         NOT NULL,
    role             text        NOT NULL CHECK (role IN ('user', 'assistant')),
    -- question: an analyst's question; tool_results: the agent's lookups;
    -- reply: what the model wrote (text, thinking, tool calls); stopped: a
    -- placeholder after the analyst pressed Stop.
    kind             text        NOT NULL CHECK (kind IN ('question', 'tool_results', 'reply', 'stopped')),
    content          text        NOT NULL,
    display          jsonb,
    meta             jsonb,
    superseded_at    timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (investigation_id, seq)
);
