ALTER TABLE task_pr_review_state DROP COLUMN last_conflict_sha;

ALTER TABLE tasks DROP COLUMN pr_mergeable;
