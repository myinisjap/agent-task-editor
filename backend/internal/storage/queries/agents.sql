-- name: ListAgentConfigs :many
SELECT * FROM agent_configs WHERE enabled = 1 ORDER BY priority ASC, created_at DESC;

-- name: ListAllAgentConfigs :many
SELECT * FROM agent_configs ORDER BY created_at DESC;

-- name: ListAllAgentConfigsPage :many
-- Cursor-paginated agent-config listing (all, enabled or not), newest first.
-- Positional params (?1 after cursor = created_at then id of last row, ?2
-- limit) sidestep the sqlc SQLite byte-offset bug; keep this comment
-- ASCII-only. Ordering is (created_at, id) descending so the cursor is a
-- stable total order, matching SearchTasksPage/ListAgentLogsPage. Used only
-- by the HTTP List handler; ListAgentConfigs (the internal enabled-only
-- lookup) is deliberately left unpaginated.
SELECT a.* FROM agent_configs a
WHERE (
    ?1 = ''
    OR a.created_at < (SELECT created_at FROM agent_configs WHERE id = ?1)
    OR (a.created_at = (SELECT created_at FROM agent_configs WHERE id = ?1) AND a.id < ?1)
  )
ORDER BY a.created_at DESC, a.id DESC
LIMIT ?2;

-- name: GetAgentConfig :one
SELECT * FROM agent_configs WHERE id = ?;

-- name: CreateAgentConfig :one
INSERT INTO agent_configs (id, name, provider_config_id, system_prompt, labels, max_tokens, timeout_secs, max_turns, enabled_plugins, enabled_mcp_servers, command_allowlist, command_denylist, max_retries, retry_backoff_secs, resume_sessions, subtasks_enabled, max_subtasks, max_cost_usd, priority, effort)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateAgentConfig :one
UPDATE agent_configs
SET name = ?, provider_config_id = ?, system_prompt = ?, labels = ?,
    max_tokens = ?, timeout_secs = ?, max_turns = ?, enabled = ?, enabled_plugins = ?, enabled_mcp_servers = ?,
    command_allowlist = ?, command_denylist = ?,
    max_retries = ?, retry_backoff_secs = ?, resume_sessions = ?,
    subtasks_enabled = ?, max_subtasks = ?, max_cost_usd = ?, priority = ?,
    effort = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: DeleteAgentConfig :exec
DELETE FROM agent_configs WHERE id = ?;
