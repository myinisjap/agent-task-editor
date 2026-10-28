-- name: SetWorkflowLabelAutonomy :exec
UPDATE workflow_labels
SET autonomy_mode = ?, autonomy_ask_on = ?, autonomy_hold_seconds = ?, autonomy_max_cost_usd = ?
WHERE id = ?;

-- name: SetTaskAlwaysAsk :execrows
UPDATE tasks SET always_ask = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: CreateAutonomyDecision :exec
INSERT INTO autonomy_decisions (id, task_id, workflow_id, label, run_id, mode, decision, reasons, outcome, hold_seconds, due_at, note)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLatestAutonomyDecision :one
SELECT * FROM autonomy_decisions
WHERE task_id = ? AND label = ?
ORDER BY created_at DESC, rowid DESC
LIMIT 1;

-- name: GetAutonomyDecision :one
SELECT * FROM autonomy_decisions WHERE id = ?;

-- name: ListDueAutonomyApprovals :many
SELECT * FROM autonomy_decisions
WHERE outcome = 'pending' AND due_at <= ?1
ORDER BY due_at, rowid;

-- name: ClaimAutonomyApproval :execrows
UPDATE autonomy_decisions
SET outcome = 'approved', outcome_at = ?1, outcome_actor = 'policy'
WHERE id = ?2 AND outcome = 'pending';

-- name: SetAutonomyOutcome :exec
UPDATE autonomy_decisions
SET outcome = ?1, note = COALESCE(?2, note), outcome_at = ?3, outcome_actor = ?4
WHERE id = ?5;

-- name: HoldPendingAutonomy :execrows
UPDATE autonomy_decisions
SET outcome = 'held', outcome_at = ?1, outcome_actor = ?2
WHERE task_id = ?3 AND label = ?4 AND outcome = 'pending';

-- name: SupersedePendingAutonomy :exec
UPDATE autonomy_decisions
SET outcome = 'superseded', outcome_at = ?1
WHERE task_id = ?2 AND label <> ?3 AND outcome = 'pending';

-- name: ListPendingAutonomyApprovals :many
SELECT d.* FROM autonomy_decisions d
JOIN tasks t ON t.id = d.task_id AND t.label = d.label
WHERE d.outcome = 'pending' AND t.archived = 0;

-- name: ListRecentPolicyApprovals :many
SELECT * FROM autonomy_decisions
WHERE outcome = 'approved' AND outcome_at >= ?1
ORDER BY outcome_at DESC, rowid DESC;

-- name: GetLatestApprovedAutonomyDecision :one
SELECT * FROM autonomy_decisions
WHERE task_id = ? AND outcome = 'approved'
ORDER BY outcome_at DESC, rowid DESC
LIMIT 1;

-- name: CreateAutonomyEscape :exec
INSERT INTO autonomy_escapes (id, task_id, workflow_id, label, source)
VALUES (?, ?, ?, ?, ?);

-- name: ListAutonomyEscapesForTask :many
SELECT * FROM autonomy_escapes WHERE task_id = ? ORDER BY created_at, rowid;

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
