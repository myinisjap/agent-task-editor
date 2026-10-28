-- name: CountTasksDoneInRange :one
-- Distinct non-archived tasks with a task_label_history row entering a
-- terminal label (workflow_labels.is_terminal != 0, resolved against the
-- task's workflow) within [range_start, range_end).
SELECT COUNT(DISTINCT h.task_id) AS count
FROM task_label_history h
JOIN tasks t ON t.id = h.task_id
JOIN workflow_labels wl ON wl.workflow_id = t.workflow_id AND wl.name = h.to_label
WHERE t.archived = 0
  AND wl.is_terminal != 0
  AND h.created_at >= CAST(sqlc.arg(range_start) AS TEXT)
  AND h.created_at <  CAST(sqlc.arg(range_end) AS TEXT);

-- name: CountVerificationsInRange :one
-- Verification results recorded within [range_start, range_end).
SELECT COUNT(*) AS total,
       CAST(COALESCE(SUM(CASE WHEN status = 'pass' THEN 1 ELSE 0 END), 0) AS INTEGER) AS passed
FROM run_verifications
WHERE created_at >= CAST(sqlc.arg(range_start) AS TEXT)
  AND created_at <  CAST(sqlc.arg(range_end) AS TEXT);

-- name: CountRunsStartedInRange :one
-- Agent runs whose started_at is within [range_start, range_end).
SELECT COUNT(*) AS count
FROM agent_runs
WHERE started_at >= CAST(sqlc.arg(range_start) AS TEXT)
  AND started_at <  CAST(sqlc.arg(range_end) AS TEXT);
