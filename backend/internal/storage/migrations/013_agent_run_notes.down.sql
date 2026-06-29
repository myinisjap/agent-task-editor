-- SQLite does not support DROP COLUMN in older versions; recreate without notes
CREATE TABLE agent_runs_backup AS SELECT id, task_id, agent_config_id, status, feedback, started_at, completed_at, created_at, stored_info FROM agent_runs;
DROP TABLE agent_runs;
ALTER TABLE agent_runs_backup RENAME TO agent_runs;
