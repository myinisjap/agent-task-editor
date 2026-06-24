-- name: ListWorkflows :many
SELECT * FROM workflows ORDER BY created_at DESC;

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

-- name: ListWorkflowLabels :many
SELECT * FROM workflow_labels WHERE workflow_id = ? ORDER BY sort_order ASC;

-- name: CreateWorkflowLabel :one
INSERT INTO workflow_labels (id, workflow_id, name, color, sort_order, agent_ignore, is_terminal, is_rejection_target)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, workflow_id, name, color, sort_order, agent_ignore, is_terminal, is_rejection_target;

-- name: GetWorkflowRejectionLabel :one
SELECT id, workflow_id, name, color, sort_order, agent_ignore, is_terminal, is_rejection_target
FROM workflow_labels
WHERE workflow_id = ? AND is_rejection_target = 1
LIMIT 1;

-- name: DeleteWorkflowLabels :exec
DELETE FROM workflow_labels WHERE workflow_id = ?;

-- name: ListWorkflowTransitions :many
SELECT * FROM workflow_transitions WHERE workflow_id = ?;

-- name: GetWorkflowTransition :one
SELECT * FROM workflow_transitions
WHERE workflow_id = ? AND from_label = ? AND to_label = ?;

-- name: CreateWorkflowTransition :one
INSERT INTO workflow_transitions (id, workflow_id, from_label, to_label, trigger_type, agent_config_id)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DeleteWorkflowTransitions :exec
DELETE FROM workflow_transitions WHERE workflow_id = ?;

-- name: CountWorkflows :one
SELECT COUNT(*) FROM workflows;
