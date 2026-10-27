-- name: CreateAgentConfigVersion :one
-- Appends the next version number for the config.
INSERT INTO agent_config_versions (id, agent_config_id, version, snapshot, actor_id, note)
VALUES (?1, ?2, (SELECT COALESCE(MAX(version), 0) + 1 FROM agent_config_versions WHERE agent_config_id = ?2), ?3, ?4, ?5)
RETURNING *;

-- name: ListAgentConfigVersions :many
SELECT * FROM agent_config_versions WHERE agent_config_id = ? ORDER BY version DESC;

-- name: GetAgentConfigVersion :one
SELECT * FROM agent_config_versions WHERE agent_config_id = ? AND version = ?;

-- name: GetLatestAgentConfigVersionNumber :one
SELECT CAST(COALESCE(MAX(version), 0) AS INTEGER) FROM agent_config_versions WHERE agent_config_id = ?;
