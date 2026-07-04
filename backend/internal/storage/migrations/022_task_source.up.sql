-- Task source tracking: where a task was imported from (e.g. a GitHub issue).
-- source is the source kind ("github"); source_ref identifies the external
-- item within that source ("owner/repo#123"). Both empty for manually
-- created tasks. The partial unique index prevents duplicate imports.
ALTER TABLE tasks ADD COLUMN source TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN source_ref TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_tasks_source_ref ON tasks(source, source_ref) WHERE source != '';

-- Per-repo GitHub Issues import settings. issue_sync_label filters which
-- open issues are imported (empty = all open issues).
ALTER TABLE repos ADD COLUMN issue_sync_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repos ADD COLUMN issue_sync_label TEXT NOT NULL DEFAULT '';
