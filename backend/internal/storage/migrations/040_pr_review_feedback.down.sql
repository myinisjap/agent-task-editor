DROP INDEX IF EXISTS idx_review_comments_external;
DROP TABLE IF EXISTS task_pr_review_state;
ALTER TABLE task_review_comments DROP COLUMN source;
ALTER TABLE task_review_comments DROP COLUMN external_id;
ALTER TABLE repos DROP COLUMN pr_review_auto_transition_enabled;
