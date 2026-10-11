-- name: UpsertAgentRateLimit :exec
INSERT INTO agent_rate_limits (agent_config_id, blocked_until, attempts)
VALUES (?, ?, ?)
ON CONFLICT (agent_config_id) DO UPDATE
SET blocked_until = excluded.blocked_until, attempts = excluded.attempts, updated_at = CURRENT_TIMESTAMP;

-- name: ListActiveAgentRateLimits :many
SELECT agent_config_id, blocked_until, attempts, updated_at
FROM agent_rate_limits WHERE blocked_until > ?;

-- name: DeleteAgentRateLimit :exec
DELETE FROM agent_rate_limits WHERE agent_config_id = ?;

-- name: PruneExpiredAgentRateLimits :execrows
DELETE FROM agent_rate_limits WHERE blocked_until <= ?;
