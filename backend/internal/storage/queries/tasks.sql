-- name: ListTasks :many
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed FROM tasks ORDER BY created_at DESC;

-- name: SearchTasks :many
-- Filterable task listing. Every filter is optional: an empty string means
-- "no filter" for that dimension. @archived is tri-state: '' hides archived
-- tasks (the default board view), 'only' returns just archived tasks, and
-- 'all' returns everything.
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed FROM tasks
WHERE (@query = '' OR title LIKE '%' || @query || '%' OR description LIKE '%' || @query || '%')
  AND (@label = '' OR label = @label)
  AND (@repo_id = '' OR repo_id = @repo_id)
  AND (@type = '' OR type = @type)
  AND (@git_state = '' OR git_state = @git_state)
  AND (
    (@archived = '' AND archived = 0)
    OR (@archived = 'only' AND archived != 0)
    OR @archived = 'all'
  )
ORDER BY created_at DESC;

-- name: SearchTasksPage :many
-- Cursor-paginated variant of SearchTasks. Positional params are used instead
-- of @named ones to sidestep a byte-offset bug in sqlc's SQLite analyzer that
-- corrupts long named-parameter queries. Argument order:
--   query, query, label, label, repo_id, repo_id, type, type,
--   git_state, git_state, archived, archived, archived,
--   after, after (cursor: created_at then id of the last row), limit.
-- Ordering is (created_at, id) descending so the cursor is a stable total order.
SELECT t.* FROM tasks t
WHERE (?1 = '' OR t.title LIKE '%' || ?1 || '%' OR t.description LIKE '%' || ?1 || '%')
  AND (?2 = '' OR t.label = ?2)
  AND (?3 = '' OR t.repo_id = ?3)
  AND (?4 = '' OR t.type = ?4)
  AND (?5 = '' OR t.git_state = ?5)
  AND (
    (?6 = '' AND t.archived = 0)
    OR (?6 = 'only' AND t.archived != 0)
    OR ?6 = 'all'
  )
  AND (
    ?7 = ''
    OR t.created_at < (SELECT created_at FROM tasks WHERE id = ?7)
    OR (t.created_at = (SELECT created_at FROM tasks WHERE id = ?7) AND t.id < ?7)
  )
ORDER BY t.created_at DESC, t.id DESC
LIMIT ?8;

-- name: GetTask :one
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed FROM tasks WHERE id = ?;

-- name: CreateTask :one
INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments, priority)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: CreateSourcedTask :one
INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments, source, source_ref)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: CountTasksBySource :one
SELECT COUNT(*) FROM tasks WHERE source = ? AND source_ref = ?;

-- name: UpdateTask :one
UPDATE tasks
SET title = ?, description = ?, type = ?, repo_id = ?, max_cost_usd = ?, priority = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: UpdateTaskLabel :one
UPDATE tasks
SET label = ?, current_agent_run_id = ?, active_agent_run_id = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: SetTaskActiveRun :exec
UPDATE tasks
SET current_agent_run_id = ?, active_agent_run_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: SetTaskWorktree :exec
UPDATE tasks
SET branch = ?, worktree_path = ?, base_ref = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: ClearActiveAgentRun :exec
UPDATE tasks
SET active_agent_run_id = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteTask :exec
DELETE FROM tasks WHERE id = ?;

-- name: ListGhSyncEligibleTasks :many
-- Tasks worth polling GitHub for PR status: branch-bearing, not archived, and
-- not in a terminal PR state (pr_merged / pr_closed). Filtering here instead of
-- in Go keeps the number of `gh` calls per sweep bounded by open work.
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed FROM tasks WHERE branch != '' AND archived = 0 AND git_state NOT IN ('pr_merged', 'pr_closed') ORDER BY created_at DESC;

-- name: ListTasksByLabel :many
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed FROM tasks WHERE label = ? ORDER BY created_at DESC;

-- name: ListAgentPickupTasks :many
SELECT t.id, t.title, t.description, t.type, t.label, t.repo_id, t.workflow_id, t.current_agent_run_id, t.agent_notes, t.active_agent_run_id, t.created_at, t.updated_at, t.branch, t.worktree_path, t.base_ref, t.attachments, t.git_state, t.paused, t.transient_retry_count, t.next_retry_at, t.source, t.source_ref, t.archived, t.pr_url, t.parent_task_id, t.created_by_run_id, t.merge_status, t.max_cost_usd, t.priority, t.writeback_in_progress_sent, t.writeback_pr_commented, t.writeback_closed FROM tasks t
WHERE t.label IN (
    SELECT wt.from_label FROM workflow_transitions wt
    WHERE wt.workflow_id = t.workflow_id
      AND wt.trigger_type IN ('agent', 'both')
)
AND t.label NOT IN (
    SELECT wl.name FROM workflow_labels wl
    WHERE wl.workflow_id = t.workflow_id
      AND wl.agent_ignore != 0
)
AND t.active_agent_run_id IS NULL
AND t.paused = 0
AND t.archived = 0
-- Compare through datetime(): the sqlite3 driver stores time.Time as RFC3339
-- with a timezone offset (e.g. 2026-07-13T20:57:44-05:00), while
-- CURRENT_TIMESTAMP is a space-separated UTC string. A raw string comparison of
-- the two formats is wrong (a future local time can sort below a UTC now),
-- letting a backed-off task be re-dispatched immediately. datetime() normalizes
-- both to canonical UTC 'YYYY-MM-DD HH:MM:SS' so the comparison is correct.
AND (t.next_retry_at IS NULL OR datetime(t.next_retry_at) <= datetime('now'))
-- Dependency gate: never dispatch a task that still has an unsatisfied blocker.
-- A blocker is satisfied once it is archived or sits on a terminal label.
AND NOT EXISTS (
    SELECT 1 FROM task_dependencies d
    JOIN tasks dt ON dt.id = d.depends_on_task_id
    WHERE d.task_id = t.id
      AND dt.archived = 0
      AND NOT EXISTS (
          SELECT 1 FROM workflow_labels wl
          WHERE wl.workflow_id = dt.workflow_id
            AND wl.name = dt.label
            AND wl.is_terminal != 0
      )
)
ORDER BY t.priority DESC, t.created_at ASC;

-- name: SetTaskTransientRetry :one
UPDATE tasks
SET transient_retry_count = ?, next_retry_at = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: ResetTaskTransientRetry :one
UPDATE tasks
SET transient_retry_count = 0, next_retry_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: UpdateTaskNotes :one
UPDATE tasks
SET agent_notes = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: UpdateTaskAttachments :one
UPDATE tasks
SET attachments = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: UpdateTaskGitState :one
UPDATE tasks
SET git_state = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: SetTaskPaused :one
UPDATE tasks
SET paused = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: SetTaskArchived :one
UPDATE tasks
SET archived = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: SetTaskPR :one
UPDATE tasks
SET git_state = ?, pr_url = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: SetTaskWritebackInProgress :exec
-- Marks that the "agent-in-progress" label write-back has been applied (or
-- best-effort attempted) for this task, so it is never retried.
UPDATE tasks SET writeback_in_progress_sent = 1 WHERE id = ?;

-- name: SetTaskWritebackPRCommented :exec
-- Marks that the "PR opened" comment write-back has been posted for this task.
UPDATE tasks SET writeback_pr_commented = 1 WHERE id = ?;

-- name: SetTaskWritebackClosed :exec
-- Marks that the source issue has been closed (with a comment) after this
-- task's PR merged.
UPDATE tasks SET writeback_closed = 1 WHERE id = ?;

-- name: CreateSubtask :one
-- Creates a child task under a parent. parent_task_id groups it; created_by_run_id
-- records the agent run that requested it. Inherits the parent's repo + workflow.
INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments, parent_task_id, created_by_run_id)
VALUES (?, ?, ?, ?, ?, ?, ?, '[]', ?, ?)
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: ListSubtasks :many
-- Direct children of a parent task, newest first.
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed FROM tasks WHERE parent_task_id = ? ORDER BY created_at DESC;

-- name: CountSubtasks :one
SELECT COUNT(*) FROM tasks WHERE parent_task_id = ?;

-- name: SetTaskMergeStatus :one
UPDATE tasks
SET merge_status = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived, pr_url, parent_task_id, created_by_run_id, merge_status, max_cost_usd, priority, writeback_in_progress_sent, writeback_pr_commented, writeback_closed;

-- name: ListSubtaskRollups :many
-- Per-parent rollup counts, restricted to tasks that actually have children so
-- the board can render "3/5 done" badges in one query. A child counts as "done"
-- when it sits on a terminal label. Also surfaces how many children are in a
-- merge_conflict state so the parent card can flag it.
SELECT
    p.id AS parent_id,
    COUNT(c.id) AS total,
    SUM(CASE WHEN EXISTS (
        SELECT 1 FROM workflow_labels wl
        WHERE wl.workflow_id = c.workflow_id AND wl.name = c.label AND wl.is_terminal != 0
    ) THEN 1 ELSE 0 END) AS done,
    SUM(CASE WHEN c.merge_status = 'merge_conflict' THEN 1 ELSE 0 END) AS conflicts
FROM tasks p
JOIN tasks c ON c.parent_task_id = p.id
GROUP BY p.id;
