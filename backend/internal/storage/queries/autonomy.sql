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
  -- exclude PR-feedback sync moves (workflow.ActorPRFeedbackSync)
  AND (h.actor_id IS NULL OR h.actor_id != 'pr-feedback-sync')
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

-- name: CreateAutonomyEscape :one
INSERT INTO autonomy_escapes (id, task_id, workflow_id, label, source, decision_id, actor, note, rule_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListAutonomyEscapesForDecision :many
SELECT * FROM autonomy_escapes WHERE decision_id = ?1 ORDER BY created_at, rowid;

-- name: CountGateEscapesForStepDown :one
-- One bad approval reported twice counts once (COALESCE on decision_id).
SELECT COUNT(DISTINCT COALESCE(decision_id, id)) FROM autonomy_escapes
WHERE workflow_id = ?1 AND label = ?2 AND reset_by_change_id IS NULL AND created_at >= ?3;

-- name: ListGateEscapeSourcesForStepDown :many
SELECT DISTINCT source FROM autonomy_escapes
WHERE workflow_id = ?1 AND label = ?2 AND reset_by_change_id IS NULL AND created_at >= ?3
ORDER BY source;

-- name: ResetGateEscapes :exec
UPDATE autonomy_escapes SET reset_by_change_id = ?1
WHERE workflow_id = ?2 AND label = ?3 AND reset_by_change_id IS NULL;

-- name: ListGateEscapes :many
SELECT e.*, COALESCE(t.title, '') AS task_title
FROM autonomy_escapes e
LEFT JOIN tasks t ON t.id = e.task_id
WHERE e.workflow_id = ?1 AND e.label = ?2
ORDER BY e.created_at DESC, e.rowid DESC
LIMIT ?3;

-- name: ListPolicyApprovalsAtGate :many
SELECT d.*, t.title AS task_title
FROM autonomy_decisions d
JOIN tasks t ON t.id = d.task_id
WHERE d.workflow_id = ?1 AND d.label = ?2 AND d.outcome IN ('approved','undone') AND d.outcome_at >= ?3
ORDER BY d.outcome_at DESC, d.rowid DESC
LIMIT ?4;

-- name: GetLatestPolicyApprovedDecision :one
SELECT * FROM autonomy_decisions
WHERE task_id = ? AND outcome IN ('approved','undone')
ORDER BY outcome_at DESC, rowid DESC
LIMIT 1;

-- name: SupersedePendingAtGate :many
UPDATE autonomy_decisions SET outcome = 'superseded', note = ?1, outcome_at = ?2
WHERE workflow_id = ?3 AND label = ?4 AND outcome = 'pending'
RETURNING id, task_id;

-- name: StepDownLabelAutonomy :execrows
-- The conditional on the current mode is the race arbiter.
UPDATE workflow_labels SET autonomy_mode = ?1, autonomy_ask_on = ?2
WHERE workflow_id = ?3 AND name = ?4 AND autonomy_mode = ?5;

-- name: CreateAutonomyChange :one
INSERT INTO autonomy_changes (id, workflow_id, label, from_mode, to_mode, actor, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListAutonomyChanges :many
SELECT * FROM autonomy_changes WHERE workflow_id = ?1 AND label = ?2
ORDER BY created_at DESC, rowid DESC
LIMIT ?3;

-- name: CreateAskRule :one
INSERT INTO autonomy_ask_rules (id, repo_id, workflow_id, label, path_globs, task_type, min_changed_lines, created_by, source_task_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAskRule :one
SELECT * FROM autonomy_ask_rules WHERE id = ?1;

-- name: UpdateAskRule :one
UPDATE autonomy_ask_rules
SET repo_id = ?1, label = ?2, path_globs = ?3, task_type = ?4, min_changed_lines = ?5
WHERE id = ?6
RETURNING *;

-- name: DeleteAskRule :execrows
DELETE FROM autonomy_ask_rules WHERE id = ?1;

-- name: ListAskRules :many
-- Empty-string filters mean "any". A label/repo filter also returns the
-- all-gates / all-repos rules, because those apply too.
SELECT * FROM autonomy_ask_rules
WHERE (?1 = '' OR workflow_id = ?1)
  AND (?2 = '' OR label = ?2 OR label IS NULL)
  AND (?3 = '' OR repo_id = ?3 OR repo_id IS NULL)
ORDER BY created_at DESC, rowid DESC;

-- name: ListApplicableAskRules :many
SELECT * FROM autonomy_ask_rules
WHERE workflow_id = ?1 AND (label IS NULL OR label = ?2) AND (repo_id IS NULL OR repo_id = ?3)
ORDER BY created_at, rowid;

-- name: RecordAskRuleHits :exec
UPDATE autonomy_ask_rules SET hit_count = hit_count + 1, last_hit_at = sqlc.arg(at)
WHERE id IN (sqlc.slice('ids'));
