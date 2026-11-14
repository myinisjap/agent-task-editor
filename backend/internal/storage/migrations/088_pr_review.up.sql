-- Review-only PR path: review any pull request on a registered repo (one ATE
-- did not write). Per-repo opt-in and filters are explicit columns on repos
-- (ADR 0036); per-task PR metadata lives in a side table (ADR 0037).
ALTER TABLE repos ADD COLUMN pr_review_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repos ADD COLUMN pr_review_author_assocs TEXT NOT NULL DEFAULT '["OWNER","MEMBER","COLLABORATOR"]';
ALTER TABLE repos ADD COLUMN pr_review_label TEXT NOT NULL DEFAULT '';
ALTER TABLE repos ADD COLUMN pr_review_skip_drafts INTEGER NOT NULL DEFAULT 1;
ALTER TABLE repos ADD COLUMN pr_review_workflow_id TEXT REFERENCES workflows(id) ON DELETE SET NULL;
ALTER TABLE repos ADD COLUMN pr_review_target_label TEXT NOT NULL DEFAULT '';

CREATE TABLE task_pr_source (
    task_id        TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    repo_id        TEXT NOT NULL,
    pr_number      INTEGER NOT NULL,
    pr_url         TEXT NOT NULL DEFAULT '',
    pr_title       TEXT NOT NULL DEFAULT '',
    pr_body        TEXT NOT NULL DEFAULT '',
    pr_author      TEXT NOT NULL DEFAULT '',
    head_ref       TEXT NOT NULL DEFAULT '',
    head_sha       TEXT NOT NULL DEFAULT '',
    base_ref       TEXT NOT NULL DEFAULT '',
    pr_state       TEXT NOT NULL DEFAULT 'open' CHECK (pr_state IN ('open', 'closed', 'merged')),
    reviewed_sha   TEXT NOT NULL DEFAULT '',
    last_posted_at DATETIME,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE task_pr_review_posts (
    id            TEXT PRIMARY KEY,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_run_id  TEXT UNIQUE,
    head_sha      TEXT NOT NULL DEFAULT '',
    summary       TEXT NOT NULL DEFAULT '',
    comments_json TEXT NOT NULL DEFAULT '[]',
    resolved_json TEXT NOT NULL DEFAULT '[]',
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'posted')),
    posted_mode   TEXT NOT NULL DEFAULT '',
    forge_ref     TEXT NOT NULL DEFAULT '',
    seq           INTEGER NOT NULL DEFAULT 1,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    posted_at     DATETIME
);
CREATE INDEX idx_task_pr_review_posts_task ON task_pr_review_posts(task_id, status);

-- One-shot seed markers (e.g. the "PR review" workflow is seeded once per
-- install so deleting it is not undone on the next start).
CREATE TABLE seed_markers (
    name       TEXT PRIMARY KEY,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Allow the new outbox kind. SQLite cannot alter a CHECK, so swap the table.
CREATE TABLE task_side_effects_new (
    id            TEXT PRIMARY KEY,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('terminal', 'create_pr', 'leave_not_ready', 'pr_review_post')),
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
