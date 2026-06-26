PRAGMA foreign_keys = OFF;

CREATE TABLE agent_runs_old (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_config_id TEXT NOT NULL REFERENCES agent_configs(id),
    status          TEXT NOT NULL DEFAULT 'pending',
    feedback        TEXT,
    started_at      DATETIME,
    completed_at    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO agent_runs_old SELECT * FROM agent_runs;

DROP TABLE agent_runs;
ALTER TABLE agent_runs_old RENAME TO agent_runs;

PRAGMA foreign_keys = ON;
