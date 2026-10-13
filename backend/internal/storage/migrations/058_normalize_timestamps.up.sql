-- Normalize every DATETIME/TIMESTAMP column to one canonical format:
-- YYYY-MM-DD HH:MM:SS in UTC (what CURRENT_TIMESTAMP writes).
--
-- Before this migration the Go driver wrote time.Time values as
-- YYYY-MM-DD HH:MM:SS.fffffffff plus a local UTC offset suffix, while SQL
-- defaults wrote the canonical form, so string comparison and ORDER BY across
-- rows from the two paths was unreliable. From now on the Go side also writes
-- the canonical form (see storage.FormatTime).
--
-- strftime parses offsets and a trailing Z and converts to UTC. Values that
-- SQLite cannot parse (empty or garbage strings) yield NULL and are left
-- untouched. Sub-second precision is dropped.
--
-- Deliberately NOT touched: TEXT cursor columns that Go parses as RFC3339
-- (task_pr_review_state.last_review_submitted_at, last_pr_updated_at,
-- last_checks_polled_at) and task_source_comments.external_created_at, which
-- mirrors the upstream tracker timestamp.

UPDATE workflows SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE workflows SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE repos SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE tasks SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE tasks SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE tasks SET next_retry_at = strftime('%Y-%m-%d %H:%M:%S', next_retry_at)
WHERE next_retry_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', next_retry_at) IS NOT NULL AND next_retry_at <> strftime('%Y-%m-%d %H:%M:%S', next_retry_at);

UPDATE tasks SET source_state_at = strftime('%Y-%m-%d %H:%M:%S', source_state_at)
WHERE source_state_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', source_state_at) IS NOT NULL AND source_state_at <> strftime('%Y-%m-%d %H:%M:%S', source_state_at);

UPDATE task_label_history SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE agent_logs SET timestamp = strftime('%Y-%m-%d %H:%M:%S', timestamp)
WHERE timestamp IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', timestamp) IS NOT NULL AND timestamp <> strftime('%Y-%m-%d %H:%M:%S', timestamp);

UPDATE agent_runs SET started_at = strftime('%Y-%m-%d %H:%M:%S', started_at)
WHERE started_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', started_at) IS NOT NULL AND started_at <> strftime('%Y-%m-%d %H:%M:%S', started_at);

UPDATE agent_runs SET completed_at = strftime('%Y-%m-%d %H:%M:%S', completed_at)
WHERE completed_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', completed_at) IS NOT NULL AND completed_at <> strftime('%Y-%m-%d %H:%M:%S', completed_at);

UPDATE agent_runs SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE task_review_comments SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE task_review_comments SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE task_templates SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE task_templates SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE task_dependencies SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE task_schedules SET last_run_at = strftime('%Y-%m-%d %H:%M:%S', last_run_at)
WHERE last_run_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', last_run_at) IS NOT NULL AND last_run_at <> strftime('%Y-%m-%d %H:%M:%S', last_run_at);

UPDATE task_schedules SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE task_schedules SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE backup_settings SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE provider_configs SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE provider_configs SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE agent_configs SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE agent_configs SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE chat_sessions SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE chat_sessions SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE task_pr_review_state SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE log_retention_settings SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE model_pricing SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE task_source_comments SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE cost_warning_settings SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);

UPDATE intake_rules SET created_at = strftime('%Y-%m-%d %H:%M:%S', created_at)
WHERE created_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', created_at) IS NOT NULL AND created_at <> strftime('%Y-%m-%d %H:%M:%S', created_at);

UPDATE intake_rules SET updated_at = strftime('%Y-%m-%d %H:%M:%S', updated_at)
WHERE updated_at IS NOT NULL AND strftime('%Y-%m-%d %H:%M:%S', updated_at) IS NOT NULL AND updated_at <> strftime('%Y-%m-%d %H:%M:%S', updated_at);
