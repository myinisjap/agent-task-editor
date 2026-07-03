ALTER TABLE agent_configs DROP COLUMN max_retries;
ALTER TABLE agent_configs DROP COLUMN retry_backoff_secs;
ALTER TABLE tasks DROP COLUMN transient_retry_count;
ALTER TABLE tasks DROP COLUMN next_retry_at;
