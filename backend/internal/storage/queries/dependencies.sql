-- Peer task dependency edges. A task is "blocked" by an edge until the
-- depended-on task reaches a terminal label (workflow_labels.is_terminal != 0)
-- or is archived. Blocked-ness is derived at read time, never stored.

-- name: CreateTaskDependency :exec
INSERT INTO task_dependencies (task_id, depends_on_task_id)
VALUES (?, ?);

-- name: DeleteTaskDependency :exec
DELETE FROM task_dependencies
WHERE task_id = ? AND depends_on_task_id = ?;

-- name: GetTaskDependency :one
SELECT task_id, depends_on_task_id, created_at
FROM task_dependencies
WHERE task_id = ? AND depends_on_task_id = ?;

-- name: ListWorkflowDependencyEdges :many
-- All edges among tasks in a single workflow. Used for transactional cycle
-- detection (dependencies never cross workflows in v1, so this bounds the graph
-- the check has to walk).
SELECT d.task_id, d.depends_on_task_id
FROM task_dependencies d
JOIN tasks t ON t.id = d.task_id
WHERE t.workflow_id = ?;

-- name: ListTaskBlockers :many
-- Tasks this task depends on (its blockers), newest first, each with derived
-- satisfaction state.
SELECT
    d.depends_on_task_id AS task_id,
    dt.title,
    dt.label,
    dt.archived,
    CASE
        WHEN dt.archived != 0 THEN 1
        WHEN EXISTS (
            SELECT 1 FROM workflow_labels wl
            WHERE wl.workflow_id = dt.workflow_id
              AND wl.name = dt.label
              AND wl.is_terminal != 0
        ) THEN 1
        ELSE 0
    END AS satisfied
FROM task_dependencies d
JOIN tasks dt ON dt.id = d.depends_on_task_id
WHERE d.task_id = ?
ORDER BY dt.created_at DESC;

-- name: ListTaskDependents :many
-- Tasks that depend on this task (this task is blocking them), newest first.
SELECT
    d.task_id AS task_id,
    tt.title,
    tt.label,
    tt.archived
FROM task_dependencies d
JOIN tasks tt ON tt.id = d.task_id
WHERE d.depends_on_task_id = ?
ORDER BY tt.created_at DESC;

-- name: ListTaskDependencyCounts :many
-- Per-task derived edge counts, restricted to tasks that participate in at least
-- one edge so the board can render badges in one query instead of N+1. Tasks not
-- returned have zero of both.
--   blocked_by_count: this task's blockers whose edges are still unsatisfied.
--   blocking_count:   tasks that depend on this task (edge count, direction only).
SELECT
    t.id AS task_id,
    (
        SELECT COUNT(*) FROM task_dependencies d
        JOIN tasks dt ON dt.id = d.depends_on_task_id
        WHERE d.task_id = t.id
          AND dt.archived = 0
          AND NOT EXISTS (
              SELECT 1 FROM workflow_labels wl
              WHERE wl.workflow_id = dt.workflow_id
                AND wl.name = dt.label
                AND wl.is_terminal != 0
          )
    ) AS blocked_by_count,
    (
        SELECT COUNT(*) FROM task_dependencies d2
        WHERE d2.depends_on_task_id = t.id
    ) AS blocking_count
FROM tasks t
WHERE EXISTS (SELECT 1 FROM task_dependencies d3 WHERE d3.task_id = t.id OR d3.depends_on_task_id = t.id);
