-- Peer task dependency edges. A task is "blocked" by an edge until the
-- depended-on task reaches a terminal label (workflow_labels.is_terminal != 0)
-- or is archived (a blocker that is a subtask must also have landed: see the
-- subtask_landed view. Home child: merge_status merged/merge_conflict. Foreign
-- child, ADR 0041: PR open or merged and latest verify passed). Blocked-ness is derived at read time, never stored.

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
              AND (dt.parent_task_id IS NULL OR EXISTS (SELECT 1 FROM subtask_landed sl WHERE sl.task_id = dt.id))
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
                AND (dt.parent_task_id IS NULL OR EXISTS (SELECT 1 FROM subtask_landed sl WHERE sl.task_id = dt.id))
          )
    ) AS blocked_by_count,
    (
        SELECT COUNT(*) FROM task_dependencies d2
        WHERE d2.depends_on_task_id = t.id
    ) AS blocking_count
FROM tasks t
WHERE EXISTS (SELECT 1 FROM task_dependencies d3 WHERE d3.task_id = t.id OR d3.depends_on_task_id = t.id);

-- name: ListTaskDependencyCountsForTasks :many
-- Same as ListTaskDependencyCounts but scoped to a specific set of task ids,
-- so callers that already know which tasks they're rendering (a single task,
-- or one page of a list) don't pay for a query that scans every task with an
-- edge, regardless of whether it's on the current page.
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
                AND (dt.parent_task_id IS NULL OR EXISTS (SELECT 1 FROM subtask_landed sl WHERE sl.task_id = dt.id))
          )
    ) AS blocked_by_count,
    (
        SELECT COUNT(*) FROM task_dependencies d2
        WHERE d2.depends_on_task_id = t.id
    ) AS blocking_count
FROM tasks t
WHERE t.id IN (sqlc.slice('task_ids'))
  AND EXISTS (SELECT 1 FROM task_dependencies d3 WHERE d3.task_id = t.id OR d3.depends_on_task_id = t.id);

-- name: ListUnsatisfiedBlockersForTasks :many
-- Batched mirror of the NOT EXISTS dependency gate in ListAgentPickupTasks:
-- for every task in task_ids, returns one row per still-unsatisfied blocker
-- (a depended-on task that is neither archived nor sitting on a terminal
-- label). Used by the read-time block-reason resolver to both detect the
-- "dependency" gate and populate its Detail (blocking task ids/titles) for a
-- whole page in a single query, instead of one ListTaskBlockers call per task.
SELECT
    d.task_id AS task_id,
    d.depends_on_task_id AS blocker_task_id,
    dt.title AS blocker_title,
    dt.label AS blocker_label,
    dt.parent_task_id AS blocker_parent_id,
    dt.repo_id AS blocker_repo_id,
    dt.merge_status AS blocker_merge_status,
    bp.repo_id AS blocker_parent_repo_id,
    CAST(EXISTS (SELECT 1 FROM workflow_labels wt WHERE wt.workflow_id = dt.workflow_id AND wt.name = dt.label AND wt.is_terminal != 0) AS INTEGER) AS blocker_terminal
FROM task_dependencies d
JOIN tasks dt ON dt.id = d.depends_on_task_id
LEFT JOIN tasks bp ON bp.id = dt.parent_task_id
WHERE d.task_id IN (sqlc.slice('task_ids'))
  AND dt.archived = 0
  AND NOT EXISTS (
      SELECT 1 FROM workflow_labels wl
      WHERE wl.workflow_id = dt.workflow_id
        AND wl.name = dt.label
        AND wl.is_terminal != 0
        AND (dt.parent_task_id IS NULL OR EXISTS (SELECT 1 FROM subtask_landed sl WHERE sl.task_id = dt.id))
  )
ORDER BY d.task_id, dt.created_at ASC;
