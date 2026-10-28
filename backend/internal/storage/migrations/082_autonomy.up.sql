-- Autonomy dial: per-gate policy on workflow labels, per-task always_ask
-- override, and shadow decisions recorded when a task lands on a gate.
ALTER TABLE workflow_labels ADD COLUMN autonomy_mode TEXT NOT NULL DEFAULT 'ask' CHECK (autonomy_mode IN ('ask','on_flags','auto'));
ALTER TABLE workflow_labels ADD COLUMN autonomy_ask_on TEXT NOT NULL DEFAULT '[]';
ALTER TABLE workflow_labels ADD COLUMN autonomy_hold_seconds INTEGER NOT NULL DEFAULT 600;
ALTER TABLE workflow_labels ADD COLUMN autonomy_max_cost_usd REAL;

ALTER TABLE tasks ADD COLUMN always_ask INTEGER NOT NULL DEFAULT 0;

-- ADR 0037: a new tasks column means drop + recreate tasks_full.
DROP VIEW tasks_full;
CREATE VIEW tasks_full AS
SELECT
    t.id,
    t.title,
    t.description,
    t.type,
    t.label,
    t.repo_id,
    t.workflow_id,
    t.current_agent_run_id,
    t.agent_notes,
    t.active_agent_run_id,
    t.created_at,
    t.updated_at,
    CAST(COALESCE(g.branch, '') AS TEXT) AS branch,
    CAST(COALESCE(g.worktree_path, '') AS TEXT) AS worktree_path,
    CAST(COALESCE(g.base_ref, '') AS TEXT) AS base_ref,
    t.attachments,
    CAST(COALESCE(g.git_state, '') AS TEXT) AS git_state,
    t.paused,
    t.transient_retry_count,
    t.next_retry_at,
    t.source,
    t.source_ref,
    t.archived,
    CAST(COALESCE(g.pr_url, '') AS TEXT) AS pr_url,
    t.parent_task_id,
    t.created_by_run_id,
    t.merge_status,
    t.max_cost_usd,
    t.priority,
    CAST(COALESCE(w.writeback_in_progress_sent, 0) AS INTEGER) AS writeback_in_progress_sent,
    CAST(COALESCE(w.writeback_pr_commented, 0) AS INTEGER) AS writeback_pr_commented,
    CAST(COALESCE(w.writeback_closed, 0) AS INTEGER) AS writeback_closed,
    t.source_state,
    t.source_state_at,
    CAST(COALESCE(g.pr_mergeable, '') AS TEXT) AS pr_mergeable,
    t.cost_warned,
    t.matched_rule_id,
    t.consecutive_failure_count,
    t.always_ask
FROM tasks t
LEFT JOIN task_git g ON g.task_id = t.id
LEFT JOIN task_writeback w ON w.task_id = t.id;

CREATE TABLE autonomy_decisions (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    workflow_id TEXT NOT NULL,
    label       TEXT NOT NULL,
    run_id      TEXT REFERENCES agent_runs(id) ON DELETE SET NULL,
    mode        TEXT NOT NULL CHECK (mode IN ('ask','on_flags','auto')),
    decision    TEXT NOT NULL CHECK (decision IN ('approve','ask')),
    reasons     TEXT NOT NULL DEFAULT '[]',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_autonomy_decisions_task_label ON autonomy_decisions(task_id, label, created_at);
