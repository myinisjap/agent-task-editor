-- name: SetWorkflowLabelAutonomy :exec
UPDATE workflow_labels
SET autonomy_mode = ?, autonomy_ask_on = ?, autonomy_hold_seconds = ?, autonomy_max_cost_usd = ?
WHERE id = ?;

-- name: SetTaskAlwaysAsk :execrows
UPDATE tasks SET always_ask = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: CreateAutonomyDecision :exec
INSERT INTO autonomy_decisions (id, task_id, workflow_id, label, run_id, mode, decision, reasons)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLatestAutonomyDecision :one
SELECT * FROM autonomy_decisions
WHERE task_id = ? AND label = ?
ORDER BY created_at DESC, rowid DESC
LIMIT 1;

-- name: ListGateExits :many
-- Human-triggered, non-forced exits from a gate label for tasks of one
-- workflow since a cutoff (canonical UTC 'YYYY-MM-DD HH:MM:SS'). Newest
-- first so LIMIT keeps the most recent passes. Read-only.
SELECT h.id AS history_id, h.task_id AS task_id, h.to_label AS to_label,
       h.created_at AS created_at, t.title AS title
FROM task_label_history h
JOIN tasks t ON t.id = h.task_id
WHERE t.workflow_id = sqlc.arg(workflow_id)
  AND h.from_label = sqlc.arg(label)
  AND h.trigger = 'human'
  AND (h.note IS NULL OR h.note NOT LIKE 'Forced move%')
  AND h.created_at >= CAST(sqlc.arg(since) AS TEXT)
ORDER BY h.created_at DESC, h.rowid DESC
LIMIT sqlc.arg(max_rows);

-- name: GetLastRunBefore :one
-- Most recent run of a task created at or before the cutoff.
SELECT id FROM agent_runs
WHERE task_id = sqlc.arg(task_id) AND created_at <= CAST(sqlc.arg(until) AS TEXT)
ORDER BY created_at DESC, rowid DESC
LIMIT 1;

-- name: SumTaskCostBefore :one
-- Cost of every run of a task created at or before the cutoff.
SELECT CAST(COALESCE(SUM(cost_usd),0) AS REAL) AS cost_usd
FROM agent_runs
WHERE task_id = sqlc.arg(task_id) AND created_at <= CAST(sqlc.arg(until) AS TEXT);
