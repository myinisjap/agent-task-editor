-- Singleton settings row controlling the automatic local-backup scheduler
-- (see internal/backup.Scheduler). id is always 1 -- a CHECK constraint
-- enforces this is a single-row table, upserted via
-- UpsertBackupSettings rather than inserted freely.
--
-- interval_seconds/keep start out mirroring config.Defaults()' BACKUP_INTERVAL
-- (24h = 86400s) and BACKUP_KEEP (7) so the DB-backed settings path exposed
-- via GET/PUT /api/v1/backup/settings surfaces the same out-of-the-box
-- defaults as the env-var-only configuration it replaces, rather than an
-- empty/zero row on first read.
CREATE TABLE backup_settings (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    interval_seconds INTEGER NOT NULL DEFAULT 86400,
    keep            INTEGER NOT NULL DEFAULT 7,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO backup_settings (id, interval_seconds, keep) VALUES (1, 86400, 7);
