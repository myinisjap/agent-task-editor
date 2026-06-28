-- name: ListTasks :many
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments FROM tasks ORDER BY created_at DESC;

-- name: GetTask :one
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments FROM tasks WHERE id = ?;

-- name: CreateTask :one
INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments;

-- name: UpdateTask :one
UPDATE tasks
SET title = ?, description = ?, type = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments;

-- name: UpdateTaskLabel :one
UPDATE tasks
SET label = ?, current_agent_run_id = ?, active_agent_run_id = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments;

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
SELECT id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments FROM tasks WHERE label = ? ORDER BY created_at DESC;

-- name: ListAgentPickupTasks :many
SELECT t.id, t.title, t.description, t.type, t.label, t.repo_id, t.workflow_id, t.current_agent_run_id, t.agent_notes, t.active_agent_run_id, t.created_at, t.updated_at, t.branch, t.worktree_path, t.base_ref, t.attachments FROM tasks t
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
AND t.active_agent_run_id IS NULL;

-- name: UpdateTaskNotes :one
UPDATE tasks
SET agent_notes = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments;

-- name: UpdateTaskAttachments :one
UPDATE tasks
SET attachments = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, title, description, type, label, repo_id, workflow_id, current_agent_run_id, agent_notes, active_agent_run_id, created_at, updated_at, branch, worktree_path, base_ref, attachments;
