DROP INDEX idx_tasks_source_ref;
ALTER TABLE tasks DROP COLUMN source;
ALTER TABLE tasks DROP COLUMN source_ref;
ALTER TABLE repos DROP COLUMN issue_sync_enabled;
ALTER TABLE repos DROP COLUMN issue_sync_label;
