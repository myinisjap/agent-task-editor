-- Skills library: provider-neutral Agent Skills (a folder with SKILL.md).
-- The files live on disk under the skills dir; this table is the index.
CREATE TABLE skills (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE agent_config_skills (
    agent_config_id TEXT NOT NULL REFERENCES agent_configs(id) ON DELETE CASCADE,
    skill_id        TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_config_id, skill_id)
);
CREATE INDEX idx_agent_config_skills_skill ON agent_config_skills(skill_id);

CREATE TABLE repo_skills (
    repo_id  TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    skill_id TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    PRIMARY KEY (repo_id, skill_id)
);
CREATE INDEX idx_repo_skills_skill ON repo_skills(skill_id);
