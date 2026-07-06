-- Agent-driven subtask decomposition (Mechanism 2).
--
-- parent_task_id groups a child under its parent (rollup, provenance). Deleting
-- a parent orphans its children to top-level rather than deleting them, so the
-- FK is ON DELETE SET NULL. created_by_run_id records which agent run created
-- the child. merge_status tracks a child's branch merge-back into the parent's
-- branch: '' (not a subtask / nothing to merge), 'pending', 'merged',
-- 'merge_conflict'.
ALTER TABLE tasks ADD COLUMN parent_task_id    TEXT REFERENCES tasks(id) ON DELETE SET NULL;
ALTER TABLE tasks ADD COLUMN created_by_run_id TEXT;
ALTER TABLE tasks ADD COLUMN merge_status      TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_tasks_parent ON tasks(parent_task_id);

-- Decomposition is opt-in per agent config: only configs with subtasks_enabled
-- get the create_subtask tool, and max_subtasks caps how many children a single
-- parent run may create.
ALTER TABLE agent_configs ADD COLUMN subtasks_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_configs ADD COLUMN max_subtasks     INTEGER NOT NULL DEFAULT 10;
