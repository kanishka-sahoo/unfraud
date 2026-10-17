-- An analyst can ask for a Case's Summary to be (re)written; the summarizer
-- claims those before the rest of the queue.
ALTER TABLE cases ADD COLUMN summary_requested boolean NOT NULL DEFAULT false;
