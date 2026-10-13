-- Durable outbox for the network-bound side effects of a label transition
-- (branch push + worktree teardown, pull request creation, source-issue
-- write-back). A row is inserted in the same transaction as the label change
-- and drained by a background worker with retry and backoff, so the request
-- that moved the task never waits on git or the forge and a failure is not lost.
CREATE TABLE task_side_effects (
    id            TEXT PRIMARY KEY,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('terminal', 'create_pr', 'leave_not_ready')),
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'failed')),
    attempts      INTEGER NOT NULL DEFAULT 0,
    next_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error    TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- At most one waiting row per (kind, task): enqueueing again while one is
-- still pending is a no-op, which makes the kinds idempotent.
CREATE UNIQUE INDEX idx_task_side_effects_pending ON task_side_effects(kind, task_id) WHERE status = 'pending';
CREATE INDEX idx_task_side_effects_due ON task_side_effects(status, next_at);
CREATE INDEX idx_task_side_effects_task ON task_side_effects(task_id);
