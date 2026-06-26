CREATE TABLE agent_runs_new (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_config_id TEXT REFERENCES agent_configs(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    feedback        TEXT,
    stored_info     TEXT,
    started_at      DATETIME,
    completed_at    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO agent_runs_new (id, task_id, agent_config_id, status, feedback, stored_info, started_at, completed_at, created_at)
SELECT id, task_id, agent_config_id, status, feedback, stored_info, started_at, completed_at, COALESCE(created_at, CURRENT_TIMESTAMP)
FROM agent_runs;

DROP TABLE agent_runs;
ALTER TABLE agent_runs_new RENAME TO agent_runs;
