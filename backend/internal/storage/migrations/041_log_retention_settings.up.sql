-- Singleton settings row controlling the automatic agent-log retention
-- pruner (see internal/logretention.Pruner). id is always 1 -- a CHECK
-- constraint enforces this is a single-row table, upserted via
-- UpsertLogRetentionSettings rather than inserted freely.
--
-- days/interval_seconds start out mirroring config.Defaults()'
-- LogRetentionDays (0, disabled) and LogRetentionInterval (1h = 3600s) so
-- the DB-backed settings path exposed via GET/PUT /api/v1/log-retention/settings
-- surfaces the same out-of-the-box defaults as the env-var-only
-- configuration it replaces, rather than an empty/zero row on first read.
CREATE TABLE log_retention_settings (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    days             INTEGER NOT NULL DEFAULT 0,
    interval_seconds INTEGER NOT NULL DEFAULT 3600,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO log_retention_settings (id, days, interval_seconds) VALUES (1, 0, 3600);
