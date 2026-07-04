-- name: ListTasks :many
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived FROM tasks ORDER BY created_at DESC;

-- name: SearchTasks :many
-- Filterable task listing. Every filter is optional: an empty string means
-- "no filter" for that dimension. @archived is tri-state: '' hides archived
-- tasks (the default board view), 'only' returns just archived tasks, and
-- 'all' returns everything.
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived FROM tasks
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

-- name: GetTask :one
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived FROM tasks WHERE id = ?;

-- name: CreateTask :one
INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived;

-- name: CreateSourcedTask :one
INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments, source, source_ref)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived;

-- name: CountTasksBySource :one
SELECT COUNT(*) FROM tasks WHERE source = ? AND source_ref = ?;

-- name: UpdateTask :one
UPDATE tasks
SET title = ?, description = ?, type = ?, repo_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived;

-- name: UpdateTaskLabel :one
UPDATE tasks
SET label = ?, current_agent_run_id = ?, active_agent_run_id = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived;

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

-- name: ListTasksByLabel :many
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived FROM tasks WHERE label = ? ORDER BY created_at DESC;

-- name: ListAgentPickupTasks :many
SELECT t.id, t.title, t.description, t.type, t.label, t.repo_id, t.workflow_id, t.current_agent_run_id, t.agent_notes, t.active_agent_run_id, t.created_at, t.updated_at, t.branch, t.worktree_path, t.base_ref, t.attachments, t.git_state, t.paused, t.transient_retry_count, t.next_retry_at, t.source, t.source_ref, t.archived FROM tasks t
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
AND (t.next_retry_at IS NULL OR t.next_retry_at <= CURRENT_TIMESTAMP);

-- name: SetTaskTransientRetry :one
UPDATE tasks
SET transient_retry_count = ?, next_retry_at = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived;

-- name: ResetTaskTransientRetry :one
UPDATE tasks
SET transient_retry_count = 0, next_retry_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived;

-- name: UpdateTaskNotes :one
UPDATE tasks
SET agent_notes = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived;

-- name: UpdateTaskAttachments :one
UPDATE tasks
SET attachments = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived;

-- name: UpdateTaskGitState :one
UPDATE tasks
SET git_state = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived;

-- name: SetTaskPaused :one
UPDATE tasks
SET paused = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived;

-- name: SetTaskArchived :one
UPDATE tasks
SET archived = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments, git_state, paused, transient_retry_count, next_retry_at, source, source_ref, archived;
