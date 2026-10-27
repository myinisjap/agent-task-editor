-- name: ListSkills :many
SELECT id, name, description, enabled, created_at, updated_at FROM skills ORDER BY name;

-- name: GetSkill :one
SELECT id, name, description, enabled, created_at, updated_at FROM skills WHERE id = ?;

-- name: CreateSkill :one
INSERT INTO skills (id, name, description, enabled)
VALUES (?, ?, ?, ?)
RETURNING id, name, description, enabled, created_at, updated_at;

-- name: UpdateSkill :one
UPDATE skills
SET description = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, name, description, enabled, created_at, updated_at;

-- name: DeleteSkill :exec
DELETE FROM skills WHERE id = ?;

-- name: ListAgentConfigSkills :many
SELECT s.id, s.name, s.description, s.enabled, s.created_at, s.updated_at
FROM skills s JOIN agent_config_skills a ON a.skill_id = s.id
WHERE a.agent_config_id = ? ORDER BY s.name;

-- name: ClearAgentConfigSkills :exec
DELETE FROM agent_config_skills WHERE agent_config_id = ?;

-- name: AddAgentConfigSkill :exec
INSERT OR IGNORE INTO agent_config_skills (agent_config_id, skill_id) VALUES (?, ?);

-- name: ListRepoSkills :many
SELECT s.id, s.name, s.description, s.enabled, s.created_at, s.updated_at
FROM skills s JOIN repo_skills r ON r.skill_id = s.id
WHERE r.repo_id = ? ORDER BY s.name;

-- name: ClearRepoSkills :exec
DELETE FROM repo_skills WHERE repo_id = ?;

-- name: AddRepoSkill :exec
INSERT OR IGNORE INTO repo_skills (repo_id, skill_id) VALUES (?, ?);

-- name: ListEnabledSkillsForRun :many
SELECT s.id, s.name, s.description, s.enabled, s.created_at, s.updated_at
FROM skills s
WHERE s.enabled = 1 AND (
    s.id IN (SELECT skill_id FROM agent_config_skills WHERE agent_config_id = ?1)
    OR s.id IN (SELECT skill_id FROM repo_skills WHERE repo_id = ?2)
)
ORDER BY s.name;
