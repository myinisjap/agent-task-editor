ALTER TABLE repos ADD COLUMN pr_review_auto_transition_enabled INTEGER NOT NULL DEFAULT 0;

ALTER TABLE task_review_comments ADD COLUMN external_id TEXT;
ALTER TABLE task_review_comments ADD COLUMN source TEXT NOT NULL DEFAULT 'local';

-- Note: inline review comment dedup is done via the partial unique index on
-- task_review_comments(task_id, external_id) below, not a cursor column
-- here — there is no last_comment_id column because there's nothing that
-- would ever populate it.
CREATE TABLE task_pr_review_state (
    task_id                  TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    head_sha                 TEXT NOT NULL DEFAULT '',
    last_review_submitted_at TEXT,
    last_failed_check_sha    TEXT,
    updated_at                DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_review_comments_external ON task_review_comments(task_id, external_id) WHERE external_id IS NOT NULL;
