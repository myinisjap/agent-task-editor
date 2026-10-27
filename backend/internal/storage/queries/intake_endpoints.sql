-- Intake endpoints: signed inbound webhooks (see internal/intakehook).
-- Tasks created by an endpoint have source = 'webhook' and
-- source_ref = '<endpoint_id>#<keyhash>#<nanos>' (or '<endpoint_id>#<uuid>'
-- when the endpoint has no dedupe key).

-- name: ListIntakeEndpoints :many
SELECT * FROM intake_endpoints ORDER BY created_at DESC, id ASC;

-- name: GetIntakeEndpoint :one
SELECT * FROM intake_endpoints WHERE id = ?;

-- name: CreateIntakeEndpoint :one
INSERT INTO intake_endpoints (id, name, repo_id, workflow_id, target_label, secret, enabled, title_template, description_template, dedupe_key_template)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateIntakeEndpoint :one
UPDATE intake_endpoints
SET name = ?, repo_id = ?, workflow_id = ?, target_label = ?, enabled = ?,
    title_template = ?, description_template = ?, dedupe_key_template = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: UpdateIntakeEndpointSecret :exec
UPDATE intake_endpoints SET secret = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: DeleteIntakeEndpoint :exec
DELETE FROM intake_endpoints WHERE id = ?;

-- name: TouchIntakeEndpointReceived :exec
UPDATE intake_endpoints SET last_received_at = ? WHERE id = ?;

-- name: GetOpenIntakeTaskIDByRefPrefix :one
-- Newest open (non-archived, non-terminal) webhook task whose source_ref
-- starts with '<endpoint_id>#<keyhash>#'. Same open definition as
-- HasOpenTaskForSchedule.
SELECT t.id FROM tasks t
WHERE t.source = 'webhook' AND t.source_ref LIKE sqlc.arg(prefix) || '#%' AND t.archived = 0
  AND NOT EXISTS (
      SELECT 1 FROM workflow_labels wl
      WHERE wl.workflow_id = t.workflow_id
        AND wl.name = t.label
        AND wl.is_terminal != 0
  )
ORDER BY t.created_at DESC
LIMIT 1;
