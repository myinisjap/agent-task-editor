-- Holds an upstream issue edit that arrived after a human approved the task,
-- so the sync importer does not silently rewrite what an agent was approved to
-- do. At most one row per task (the latest edit wins); a human applies it to
-- the task or discards it. Title, description and type are stored exactly as
-- the importer would have written them to the task. A discarded edit stays as
-- a dismissed row so the same upstream text is not held again on the next
-- sweep; a different later edit replaces it and is held afresh.
CREATE TABLE task_source_pending (
    task_id     TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    description TEXT NOT NULL,
    type        TEXT NOT NULL,
    dismissed   INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
