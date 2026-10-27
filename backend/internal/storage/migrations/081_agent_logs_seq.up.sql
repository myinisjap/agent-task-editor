ALTER TABLE agent_logs ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;
UPDATE agent_logs SET seq = rowid;
DROP INDEX IF EXISTS idx_agent_logs_run_timestamp;
CREATE INDEX IF NOT EXISTS idx_agent_logs_run_ts_seq ON agent_logs(agent_run_id, timestamp, seq);
