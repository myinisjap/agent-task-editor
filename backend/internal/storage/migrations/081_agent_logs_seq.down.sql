DROP INDEX IF EXISTS idx_agent_logs_run_ts_seq;
CREATE INDEX IF NOT EXISTS idx_agent_logs_run_timestamp ON agent_logs(agent_run_id, timestamp);
ALTER TABLE agent_logs DROP COLUMN seq;
