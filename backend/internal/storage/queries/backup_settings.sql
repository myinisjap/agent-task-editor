-- Singleton settings row controlling the automatic local-backup scheduler
-- (see internal/backup.Scheduler and 036_backup_settings). The scheduler
-- polls GetBackupSettings on its own timer so a change here takes effect
-- without a process restart -- see internal/backup.Scheduler.Run.

-- name: GetBackupSettings :one
SELECT id, interval_seconds, keep, updated_at FROM backup_settings WHERE id = 1;

-- name: UpsertBackupSettings :one
INSERT INTO backup_settings (id, interval_seconds, keep, updated_at)
VALUES (1, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT (id) DO UPDATE SET
    interval_seconds = excluded.interval_seconds,
    keep = excluded.keep,
    updated_at = CURRENT_TIMESTAMP
RETURNING id, interval_seconds, keep, updated_at;
