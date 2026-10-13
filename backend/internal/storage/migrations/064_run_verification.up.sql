-- Repo-level verify command. verify_command is run by the pool in the task
-- worktree when a run signals success, before the success transition; empty
-- (the default) means no verification and unchanged behavior.
-- verify_timeout_seconds bounds that command.
ALTER TABLE repos ADD COLUMN verify_command TEXT NOT NULL DEFAULT '';
ALTER TABLE repos ADD COLUMN verify_timeout_seconds INTEGER NOT NULL DEFAULT 600;

-- Per-label override of the repo verify command, applied to the label a task
-- is on when its run signals success. verify_mode is inherit (use the repo
-- command), custom (use this label verify_command instead) or disabled (do
-- not verify on this label). The repo timeout always applies.
ALTER TABLE workflow_labels ADD COLUMN verify_mode TEXT NOT NULL DEFAULT 'inherit';
ALTER TABLE workflow_labels ADD COLUMN verify_command TEXT NOT NULL DEFAULT '';

-- One verification record per run that had a verify command to run. status is
-- pass | fail | timeout | error. output holds the capped tail of the combined
-- stdout and stderr; truncated is 1 when earlier output was dropped.
CREATE TABLE run_verifications (
    run_id      TEXT PRIMARY KEY REFERENCES agent_runs(id) ON DELETE CASCADE,
    task_id     TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    command     TEXT NOT NULL,
    status      TEXT NOT NULL,
    exit_code   INTEGER,
    output      TEXT NOT NULL DEFAULT '',
    truncated   INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_run_verifications_task ON run_verifications(task_id, created_at DESC);
