-- Opt-in auto-merge policy per repo: ATE merges a task's PR itself once it is
-- safe (see ghsync auto-merge). Off by default. Values are validated by the
-- repo handlers (off|when_green; squash|merge|rebase); empty reads as the default.
ALTER TABLE repos ADD COLUMN auto_merge_mode TEXT NOT NULL DEFAULT 'off';
ALTER TABLE repos ADD COLUMN auto_merge_method TEXT NOT NULL DEFAULT 'squash';

-- Allow the new 'auto_merge' outbox kind. SQLite cannot alter a CHECK
-- constraint, so rebuild the table (rows are preserved).
CREATE TABLE task_side_effects_new (
    id            TEXT PRIMARY KEY,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('terminal', 'create_pr', 'leave_not_ready', 'auto_merge')),
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'failed')),
    attempts      INTEGER NOT NULL DEFAULT 0,
    next_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error    TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO task_side_effects_new (id, task_id, kind, status, attempts, next_at, last_error, created_at, updated_at)
SELECT id, task_id, kind, status, attempts, next_at, last_error, created_at, updated_at FROM task_side_effects;
DROP TABLE task_side_effects;
ALTER TABLE task_side_effects_new RENAME TO task_side_effects;
CREATE UNIQUE INDEX idx_task_side_effects_pending ON task_side_effects(kind, task_id) WHERE status = 'pending';
CREATE INDEX idx_task_side_effects_due ON task_side_effects(status, next_at);
CREATE INDEX idx_task_side_effects_task ON task_side_effects(task_id);
