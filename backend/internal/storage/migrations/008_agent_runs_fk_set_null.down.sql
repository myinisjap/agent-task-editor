PRAGMA foreign_keys = OFF;

-- Column list must match the schema as it existed immediately before this
-- migration's .up.sql ran, i.e. after 001_init + 006_run_stored_info (which
-- added stored_info) but before 008 (which added the ON DELETE SET NULL FK
-- and reordered stored_info next to feedback). Omitting stored_info here
-- was a bug: rolling this migration back after 006 had already run made
-- `INSERT INTO agent_runs_old SELECT * FROM agent_runs` fail with a
-- column-count mismatch (8 destination columns vs. 9 source columns).
CREATE TABLE agent_runs_old (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_config_id TEXT NOT NULL REFERENCES agent_configs(id),
    status          TEXT NOT NULL DEFAULT 'pending',
    feedback        TEXT,
    started_at      DATETIME,
    completed_at    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    stored_info     TEXT
);

INSERT INTO agent_runs_old (id, task_id, agent_config_id, status, feedback, started_at, completed_at, created_at, stored_info)
SELECT id, task_id, agent_config_id, status, feedback, started_at, completed_at, created_at, stored_info
FROM agent_runs;

DROP TABLE agent_runs;
ALTER TABLE agent_runs_old RENAME TO agent_runs;

PRAGMA foreign_keys = ON;
