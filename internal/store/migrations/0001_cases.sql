-- Every Decision, Provisional and Revised (ADR-0003). One row per Event and
-- Stage; redelivered Decisions are ignored.
CREATE TABLE decisions (
    profile       text        NOT NULL,
    event_id      text        NOT NULL,
    stage         smallint    NOT NULL, -- 1 provisional, 2 revised
    event_time    timestamptz NOT NULL,
    probability   real        NOT NULL,
    model_action  smallint    NOT NULL, -- 0 approve, 1 review, 2 block
    rule_action   smallint    NOT NULL,
    action        smallint    NOT NULL,
    reason_codes  text[]      NOT NULL DEFAULT '{}',
    model_version text        NOT NULL,
    scored_at     timestamptz NOT NULL,
    ingested_at   timestamptz NOT NULL,
    PRIMARY KEY (profile, event_id, stage)
);
CREATE INDEX decisions_scored_at ON decisions (profile, scored_at);

-- A Case is an Event that some Stage flagged (review or block). Its action
-- is the strictest so far; it is never lowered.
CREATE TABLE cases (
    profile          text        NOT NULL,
    event_id         text        NOT NULL,
    event_time       timestamptz NOT NULL,
    entities         jsonb       NOT NULL,
    action           smallint    NOT NULL,
    probability      real        NOT NULL,
    flagged_by       smallint    NOT NULL, -- Stage that first flagged it
    reason_codes     text[]      NOT NULL DEFAULT '{}',
    contributions    jsonb,                -- top Contributions of the flagging model
    model_version    text        NOT NULL,
    disposition      text CHECK (disposition IN ('confirmed', 'dismissed', 'escalated')),
    disposition_note text,
    disposed_at      timestamptz,
    summary          jsonb,
    summary_status   text        NOT NULL DEFAULT 'pending'
                     CHECK (summary_status IN ('pending', 'running', 'done', 'failed', 'skipped')),
    summary_attempts int         NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile, event_id)
);
CREATE INDEX cases_queue ON cases (profile, action DESC, probability DESC) WHERE disposition IS NULL;
CREATE INDEX cases_summary ON cases (summary_status, created_at) WHERE summary_status IN ('pending', 'running');
