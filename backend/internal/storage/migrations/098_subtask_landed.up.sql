-- Derived (ADR 0027): subtasks whose landing satisfies a dependency edge / parent readiness.
-- Home child (same repo as parent): merged back (merged or merge_conflict).
-- Foreign child (ADR 0041): PR open or merged AND latest verify passed (no verify row = pass).
-- Mirrored in Go by agent.SubtaskReadiness; keep both in sync.
CREATE VIEW subtask_landed AS
SELECT c.id AS task_id
FROM tasks c
JOIN tasks p ON p.id = c.parent_task_id
WHERE (c.repo_id = p.repo_id AND c.merge_status IN ('merged', 'merge_conflict'))
   OR (c.repo_id != p.repo_id AND c.merge_status IN ('pr_open', 'merged')
       AND COALESCE((SELECT v.status FROM run_verifications v WHERE v.task_id = c.id
                     ORDER BY v.created_at DESC, v.run_id DESC LIMIT 1), 'pass') = 'pass');
