DELETE FROM task_side_effects WHERE kind = 'review_reply';
CREATE TABLE task_side_effects_old (
    id            TEXT PRIMARY KEY,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('terminal', 'create_pr', 'leave_not_ready', 'auto_merge', 'pr_review_post')),
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'failed')),
    attempts      INTEGER NOT NULL DEFAULT 0,
    next_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error    TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO task_side_effects_old SELECT id, task_id, kind, status, attempts, next_at, last_error, created_at, updated_at FROM task_side_effects;
DROP TABLE task_side_effects;
ALTER TABLE task_side_effects_old RENAME TO task_side_effects;
CREATE UNIQUE INDEX idx_task_side_effects_pending ON task_side_effects(kind, task_id) WHERE status = 'pending';
CREATE INDEX idx_task_side_effects_due ON task_side_effects(status, next_at);
CREATE INDEX idx_task_side_effects_task ON task_side_effects(task_id);
ALTER TABLE task_review_comments DROP COLUMN reply_posted_at;
ALTER TABLE task_review_comments DROP COLUMN reply_external_id;
ALTER TABLE repos DROP COLUMN review_reply_resolve_enabled;
ALTER TABLE repos DROP COLUMN review_reply_enabled;
