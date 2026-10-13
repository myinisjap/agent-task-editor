DROP INDEX IF EXISTS idx_run_verifications_task;
DROP TABLE run_verifications;
ALTER TABLE repos DROP COLUMN verify_timeout_seconds;
ALTER TABLE repos DROP COLUMN verify_command;
ALTER TABLE workflow_labels DROP COLUMN verify_command;
ALTER TABLE workflow_labels DROP COLUMN verify_mode;
