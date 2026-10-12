-- Restore the moved columns on tasks, copy the side-table data back, and drop
-- the view and side tables.

DROP VIEW tasks_full;

ALTER TABLE tasks ADD COLUMN branch        TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN worktree_path TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN base_ref      TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN git_state     TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN pr_url        TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN pr_mergeable  TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN writeback_in_progress_sent INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN writeback_pr_commented     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN writeback_closed           INTEGER NOT NULL DEFAULT 0;

UPDATE tasks SET
    branch        = COALESCE((SELECT g.branch        FROM task_git g WHERE g.task_id = tasks.id), ''),
    worktree_path = COALESCE((SELECT g.worktree_path FROM task_git g WHERE g.task_id = tasks.id), ''),
    base_ref      = COALESCE((SELECT g.base_ref      FROM task_git g WHERE g.task_id = tasks.id), ''),
    git_state     = COALESCE((SELECT g.git_state     FROM task_git g WHERE g.task_id = tasks.id), ''),
    pr_url        = COALESCE((SELECT g.pr_url        FROM task_git g WHERE g.task_id = tasks.id), ''),
    pr_mergeable  = COALESCE((SELECT g.pr_mergeable  FROM task_git g WHERE g.task_id = tasks.id), '')
WHERE id IN (SELECT task_id FROM task_git);

UPDATE tasks SET
    writeback_in_progress_sent = COALESCE((SELECT w.writeback_in_progress_sent FROM task_writeback w WHERE w.task_id = tasks.id), 0),
    writeback_pr_commented     = COALESCE((SELECT w.writeback_pr_commented     FROM task_writeback w WHERE w.task_id = tasks.id), 0),
    writeback_closed           = COALESCE((SELECT w.writeback_closed           FROM task_writeback w WHERE w.task_id = tasks.id), 0)
WHERE id IN (SELECT task_id FROM task_writeback);

DROP TABLE task_writeback;
DROP TABLE task_git;

