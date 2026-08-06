-- name: ListWorkflows :many
SELECT * FROM workflows ORDER BY created_at DESC;

-- name: ListWorkflowsPage :many
-- Cursor-paginated workflow listing, newest first. Positional params (?1
-- after cursor = created_at then id of last row, ?2 limit) sidestep the
-- sqlc SQLite byte-offset bug; keep this comment ASCII-only. Ordering is
-- (created_at, id) descending so the cursor is a stable total order,
-- matching SearchTasksPage/ListAgentLogsPage.
SELECT w.* FROM workflows w
WHERE (
    ?1 = ''
    OR w.created_at < (SELECT created_at FROM workflows WHERE id = ?1)
    OR (w.created_at = (SELECT created_at FROM workflows WHERE id = ?1) AND w.id < ?1)
  )
ORDER BY w.created_at DESC, w.id DESC
LIMIT ?2;

-- name: GetWorkflow :one
SELECT * FROM workflows WHERE id = ?;

-- name: CreateWorkflow :one
INSERT INTO workflows (id, name, description)
VALUES (?, ?, ?)
RETURNING *;

-- name: UpdateWorkflow :one
UPDATE workflows
SET name = ?, description = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: DeleteWorkflow :exec
DELETE FROM workflows WHERE id = ?;

-- name: CountTasksByWorkflow :one
SELECT COUNT(*) FROM tasks WHERE workflow_id = ?;

-- name: ListWorkflowLabels :many
SELECT * FROM workflow_labels WHERE workflow_id = ? ORDER BY sort_order ASC;

-- name: CreateWorkflowLabel :one
INSERT INTO workflow_labels (id, workflow_id, name, color, sort_order, agent_ignore, is_terminal, create_pr, wip_limit, wip_limit_hard)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, workflow_id, name, color, sort_order, agent_ignore, is_terminal, create_pr, wip_limit, wip_limit_hard;

-- name: DeleteWorkflowLabels :exec
DELETE FROM workflow_labels WHERE workflow_id = ?;

-- name: ListWorkflowTransitions :many
SELECT * FROM workflow_transitions WHERE workflow_id = ?;

-- name: GetWorkflowTransition :one
SELECT * FROM workflow_transitions
WHERE workflow_id = ? AND from_label = ? AND to_label = ?;

-- name: CreateWorkflowTransition :one
INSERT INTO workflow_transitions (id, workflow_id, from_label, to_label, trigger_type, agent_config_id, path)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DeleteWorkflowTransitions :exec
DELETE FROM workflow_transitions WHERE workflow_id = ?;

-- name: GetWorkflowByName :one
SELECT * FROM workflows WHERE name = ?;

-- name: CountWorkflows :one
SELECT COUNT(*) FROM workflows;
