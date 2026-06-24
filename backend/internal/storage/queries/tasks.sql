-- name: ListTasks :many
SELECT * FROM tasks ORDER BY created_at DESC;

-- name: GetTask :one
SELECT * FROM tasks WHERE id = ?;

-- name: CreateTask :one
INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateTask :one
UPDATE tasks
SET title = ?, description = ?, type = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: UpdateTaskLabel :one
UPDATE tasks
SET label = ?, current_agent_run_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: DeleteTask :exec
DELETE FROM tasks WHERE id = ?;

-- name: ListTasksByLabel :many
SELECT * FROM tasks WHERE label = ? ORDER BY created_at DESC;

-- name: ListAgentPickupTasks :many
SELECT t.* FROM tasks t
WHERE t.label IN (
    SELECT wt.from_label FROM workflow_transitions wt
    WHERE wt.workflow_id = t.workflow_id
      AND wt.trigger_type IN ('agent', 'both')
)
AND (t.current_agent_run_id IS NULL OR NOT EXISTS (
    SELECT 1 FROM agent_runs ar
    WHERE ar.id = t.current_agent_run_id AND ar.status = 'running'
));
