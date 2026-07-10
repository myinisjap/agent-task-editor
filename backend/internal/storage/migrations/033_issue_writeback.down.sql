ALTER TABLE tasks DROP COLUMN writeback_closed;
ALTER TABLE tasks DROP COLUMN writeback_pr_commented;
ALTER TABLE tasks DROP COLUMN writeback_in_progress_sent;
ALTER TABLE repos DROP COLUMN issue_writeback_enabled;
