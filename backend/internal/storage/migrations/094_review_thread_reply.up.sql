-- Reply on the forge review thread when an agent resolves an ingested comment.
-- Both repo switches are opt-in.
ALTER TABLE repos ADD COLUMN review_reply_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repos ADD COLUMN review_reply_resolve_enabled INTEGER NOT NULL DEFAULT 0;

-- reply_external_id is the forge id of the reply ATE posted (also used to keep
-- ATE's own replies from being re-ingested); reply_posted_at is the
-- once-per-comment idempotency marker.
ALTER TABLE task_review_comments ADD COLUMN reply_external_id TEXT;
ALTER TABLE task_review_comments ADD COLUMN reply_posted_at DATETIME;

-- Widen the task_side_effects kind CHECK (SQLite cannot alter a CHECK).
CREATE TABLE task_side_effects_new (
    id            TEXT PRIMARY KEY,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('terminal', 'create_pr', 'leave_not_ready', 'auto_merge', 'pr_review_post', 'review_reply')),
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'failed')),
    attempts      INTEGER NOT NULL DEFAULT 0,
    next_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error    TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO task_side_effects_new SELECT id, task_id, kind, status, attempts, next_at, last_error, created_at, updated_at FROM task_side_effects;
DROP TABLE task_side_effects;
ALTER TABLE task_side_effects_new RENAME TO task_side_effects;
CREATE UNIQUE INDEX idx_task_side_effects_pending ON task_side_effects(kind, task_id) WHERE status = 'pending';
CREATE INDEX idx_task_side_effects_due ON task_side_effects(status, next_at);
CREATE INDEX idx_task_side_effects_task ON task_side_effects(task_id);
