-- Per-repo policy for keeping imported tasks in sync with their source issue.
-- issue_sync_update_policy: 'gate'   = apply upstream title/body/label drift
--                                      only while the task still sits on the
--                                      workflow's human-gate label (default)
--                           'always' = apply drift at any label
--                           'never'  = detect drift but never write to the task
ALTER TABLE repos ADD COLUMN issue_sync_update_policy TEXT NOT NULL DEFAULT 'gate';

-- What to do when an imported issue closes or stops matching the filter.
-- 'flag' (default) records the state and takes no workflow action; 'archive'
-- archives the task; 'move' transitions it to issue_sync_gone_label.
ALTER TABLE repos ADD COLUMN issue_sync_gone_action TEXT NOT NULL DEFAULT 'flag';
ALTER TABLE repos ADD COLUMN issue_sync_gone_label TEXT NOT NULL DEFAULT '';

-- Opt-in ingestion of the source issue's comment thread into the agent prompt.
ALTER TABLE repos ADD COLUMN issue_comment_sync_enabled INTEGER NOT NULL DEFAULT 0;

-- Reconciliation state for an imported task. '' = the source item was present
-- in the last sweep's fetch; 'gone' = it closed or no longer matches the filter.
ALTER TABLE tasks ADD COLUMN source_state TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN source_state_at DATETIME;

-- Append-only ingested comment thread from the task's source item. Deduped by
-- (task_id, external_id); mirrors task_review_comments' external_id approach.
CREATE TABLE task_source_comments (
    id                  TEXT PRIMARY KEY,
    task_id             TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    external_id         TEXT NOT NULL,
    author              TEXT NOT NULL,
    body                TEXT NOT NULL,
    external_created_at TEXT NOT NULL,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX idx_task_source_comments_external
    ON task_source_comments(task_id, external_id);
CREATE INDEX idx_task_source_comments_task ON task_source_comments(task_id);
