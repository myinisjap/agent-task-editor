-- Split the git/worktree/PR state and the source-issue write-back flags out of
-- the wide tasks table into two side tables keyed by task id. Rows are created
-- lazily (upserted on first write), so a task without a row simply has the
-- default values. The tasks_full view joins them back with the old defaults so
-- reads still see one flat row with the original 37 columns in the original
-- order. Only tasks that carry non-default values are copied.

CREATE TABLE task_git (
    task_id       TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    branch        TEXT NOT NULL DEFAULT '',
    worktree_path TEXT NOT NULL DEFAULT '',
    base_ref      TEXT NOT NULL DEFAULT '',
    git_state     TEXT NOT NULL DEFAULT '',
    pr_url        TEXT NOT NULL DEFAULT '',
    pr_mergeable  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE task_writeback (
    task_id                    TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    writeback_in_progress_sent INTEGER NOT NULL DEFAULT 0,
    writeback_pr_commented     INTEGER NOT NULL DEFAULT 0,
    writeback_closed           INTEGER NOT NULL DEFAULT 0
);

INSERT INTO task_git (task_id, branch, worktree_path, base_ref, git_state, pr_url, pr_mergeable)
SELECT id, branch, worktree_path, base_ref, git_state, pr_url, pr_mergeable
FROM tasks
WHERE branch != '' OR worktree_path != '' OR base_ref != '' OR git_state != ''
   OR pr_url != '' OR pr_mergeable != '';

INSERT INTO task_writeback (task_id, writeback_in_progress_sent, writeback_pr_commented, writeback_closed)
SELECT id, writeback_in_progress_sent, writeback_pr_commented, writeback_closed
FROM tasks
WHERE writeback_in_progress_sent != 0 OR writeback_pr_commented != 0 OR writeback_closed != 0;

ALTER TABLE tasks DROP COLUMN branch;
ALTER TABLE tasks DROP COLUMN worktree_path;
ALTER TABLE tasks DROP COLUMN base_ref;
ALTER TABLE tasks DROP COLUMN git_state;
ALTER TABLE tasks DROP COLUMN pr_url;
ALTER TABLE tasks DROP COLUMN pr_mergeable;
ALTER TABLE tasks DROP COLUMN writeback_in_progress_sent;
ALTER TABLE tasks DROP COLUMN writeback_pr_commented;
ALTER TABLE tasks DROP COLUMN writeback_closed;

-- Flat read model: every tasks column, in the historical column order, with the
-- side-table values coalesced to their old defaults. A later migration that
-- rebuilds or alters tasks must drop and recreate this view.
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
    t.consecutive_failure_count
FROM tasks t
LEFT JOIN task_git g ON g.task_id = t.id
LEFT JOIN task_writeback w ON w.task_id = t.id;

