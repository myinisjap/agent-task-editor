-- Archived tasks are hidden from the board by default and excluded from the
-- ghsync PR-status sweep and agent dispatch. Archiving is independent of the
-- task's label — it does not move the task through the workflow.
ALTER TABLE tasks ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
