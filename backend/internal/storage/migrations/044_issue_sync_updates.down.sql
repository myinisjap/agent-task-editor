DROP INDEX IF EXISTS idx_task_source_comments_task;
DROP INDEX IF EXISTS idx_task_source_comments_external;
DROP TABLE IF EXISTS task_source_comments;
ALTER TABLE tasks DROP COLUMN source_state_at;
ALTER TABLE tasks DROP COLUMN source_state;
ALTER TABLE repos DROP COLUMN issue_comment_sync_enabled;
ALTER TABLE repos DROP COLUMN issue_sync_gone_label;
ALTER TABLE repos DROP COLUMN issue_sync_gone_action;
ALTER TABLE repos DROP COLUMN issue_sync_update_policy;
