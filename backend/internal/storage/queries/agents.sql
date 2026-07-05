-- name: ListAgentConfigs :many
SELECT * FROM agent_configs WHERE enabled = 1 ORDER BY created_at DESC;

-- name: ListAllAgentConfigs :many
SELECT * FROM agent_configs ORDER BY created_at DESC;

-- name: GetAgentConfig :one
SELECT * FROM agent_configs WHERE id = ?;

-- name: CreateAgentConfig :one
INSERT INTO agent_configs (id, name, provider, model, system_prompt, labels, env, max_tokens, timeout_secs, max_turns, enabled_plugins, enabled_mcp_servers, command_allowlist, command_denylist, max_retries, retry_backoff_secs, resume_sessions)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateAgentConfig :one
UPDATE agent_configs
SET name = ?, provider = ?, model = ?, system_prompt = ?, labels = ?, env = ?,
    max_tokens = ?, timeout_secs = ?, max_turns = ?, enabled = ?, enabled_plugins = ?, enabled_mcp_servers = ?,
    command_allowlist = ?, command_denylist = ?,
    max_retries = ?, retry_backoff_secs = ?, resume_sessions = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: DeleteAgentConfig :exec
DELETE FROM agent_configs WHERE id = ?;
