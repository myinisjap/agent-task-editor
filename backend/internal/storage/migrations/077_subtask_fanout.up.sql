-- Planner fan-out: per-subtask file scope and per-parent integration status.
-- A side table (not new tasks columns) so the tasks_full view is untouched.
CREATE TABLE task_fanout (
    task_id            TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    file_scope         TEXT NOT NULL DEFAULT '',   -- newline-joined globs (children)
    integration_status TEXT NOT NULL DEFAULT ''    -- '' | pending | done (parents)
);

-- Terminal children that never had a branch have nothing to merge; mark them
-- merged so the stricter "subtask blocker satisfied" gate can't strand a parent.
UPDATE tasks SET merge_status = 'merged'
WHERE parent_task_id IS NOT NULL AND merge_status = ''
  AND EXISTS (SELECT 1 FROM workflow_labels wl WHERE wl.workflow_id = tasks.workflow_id AND wl.name = tasks.label AND wl.is_terminal != 0)
  AND NOT EXISTS (SELECT 1 FROM task_git g WHERE g.task_id = tasks.id AND g.branch != '');
