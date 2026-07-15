-- name: ListProviderConfigs :many
SELECT * FROM provider_configs ORDER BY created_at DESC;

-- name: GetProviderConfig :one
SELECT * FROM provider_configs WHERE id = ?;

-- name: CreateProviderConfig :one
INSERT INTO provider_configs (id, name, provider, model, env)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateProviderConfig :one
UPDATE provider_configs
SET name = ?, provider = ?, model = ?, env = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: DeleteProviderConfig :exec
DELETE FROM provider_configs WHERE id = ?;

-- name: CountAgentConfigsByProviderConfig :one
SELECT COUNT(*) FROM agent_configs WHERE provider_config_id = ?;

-- name: CountChatSessionsByProviderConfig :one
SELECT COUNT(*) FROM chat_sessions WHERE provider_config_id = ?;

-- name: ListInUseProviders :many
-- Distinct provider strings actually referenced by an *enabled* agent config
-- or by any chat session (chat sessions have no enabled/disabled concept, so
-- any session in the table counts as "in use"). Used by the /health/providers
-- check so unused/disabled provider configs don't produce noisy false-positive
-- readiness warnings (e.g. a missing API key for a provider nothing runs).
SELECT DISTINCT pc.provider FROM provider_configs pc
WHERE pc.id IN (SELECT provider_config_id FROM agent_configs WHERE enabled = 1)
   OR pc.id IN (SELECT provider_config_id FROM chat_sessions);
