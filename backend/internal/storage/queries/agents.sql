-- name: ListAgentConfigs :many
SELECT * FROM agent_configs ORDER BY created_at DESC;

-- name: GetAgentConfig :one
SELECT * FROM agent_configs WHERE id = ?;

-- name: CreateAgentConfig :one
INSERT INTO agent_configs (id, name, provider, model, system_prompt, labels, env, max_tokens, timeout_secs)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateAgentConfig :one
UPDATE agent_configs
SET name = ?, provider = ?, model = ?, system_prompt = ?, labels = ?, env = ?,
    max_tokens = ?, timeout_secs = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: DeleteAgentConfig :exec
DELETE FROM agent_configs WHERE id = ?;
