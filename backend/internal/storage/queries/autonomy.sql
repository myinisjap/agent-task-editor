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
