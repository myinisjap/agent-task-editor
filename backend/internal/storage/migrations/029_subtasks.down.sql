ALTER TABLE agent_configs DROP COLUMN max_subtasks;
ALTER TABLE agent_configs DROP COLUMN subtasks_enabled;
DROP INDEX IF EXISTS idx_tasks_parent;
ALTER TABLE tasks DROP COLUMN merge_status;
ALTER TABLE tasks DROP COLUMN created_by_run_id;
ALTER TABLE tasks DROP COLUMN parent_task_id;
