-- Singleton settings row controlling the automatic agent-log retention
-- pruner (see internal/logretention.Pruner and 041_log_retention_settings).
-- The pruner polls GetLogRetentionSettings on its own timer so a change
-- here takes effect without a process restart -- see
-- internal/logretention.Pruner.Run.

-- name: GetLogRetentionSettings :one
SELECT id, days, interval_seconds, updated_at FROM log_retention_settings WHERE id = 1;

-- name: UpsertLogRetentionSettings :one
INSERT INTO log_retention_settings (id, days, interval_seconds, updated_at)
VALUES (1, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT (id) DO UPDATE SET
    days = excluded.days,
    interval_seconds = excluded.interval_seconds,
    updated_at = CURRENT_TIMESTAMP
RETURNING id, days, interval_seconds, updated_at;
