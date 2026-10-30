-- Model-proposed ask rules and the batch runs that produce them (ADR 0040).
-- Proposals never feed autonomy.Evaluate; accepting one creates an ask rule.

-- name: CreateProposalRun :one
INSERT INTO autonomy_proposal_runs (id, workflow_id, label, trigger, actor, status, provider_config_id, started_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: FinishProposalRun :one
UPDATE autonomy_proposal_runs
SET status = ?1, evidence_count = ?2, proposal_count = ?3, flag_ideas = ?4, error = ?5,
    cost_usd = ?6, input_tokens = ?7, output_tokens = ?8, finished_at = ?9
WHERE id = ?10
RETURNING *;

-- name: GetProposalRun :one
SELECT * FROM autonomy_proposal_runs WHERE id = ?1;

-- name: ListProposalRuns :many
SELECT * FROM autonomy_proposal_runs
WHERE workflow_id = ?1 AND label = ?2
ORDER BY started_at DESC, rowid DESC
LIMIT ?3;

-- name: GetLatestProposalRun :one
SELECT * FROM autonomy_proposal_runs
WHERE workflow_id = ?1 AND label = ?2
ORDER BY started_at DESC, rowid DESC
LIMIT 1;

-- name: FailRunningProposalRuns :exec
-- Startup recovery: a run left 'running' by a crash or restart never finishes.
UPDATE autonomy_proposal_runs
SET status = 'failed', error = 'interrupted by a server restart', finished_at = ?1
WHERE status = 'running';

-- name: DeleteOldProposalRuns :execrows
DELETE FROM autonomy_proposal_runs WHERE status <> 'running' AND started_at < ?1;

-- name: GetRuleProposal :one
SELECT * FROM autonomy_rule_proposals WHERE id = ?1;

-- name: GetRuleProposalByKey :one
SELECT * FROM autonomy_rule_proposals WHERE workflow_id = ?1 AND label = ?2 AND rule_key = ?3;

-- name: ListRuleProposals :many
-- Empty-string filters mean "any".
SELECT * FROM autonomy_rule_proposals
WHERE (?1 = '' OR workflow_id = ?1)
  AND (?2 = '' OR label = ?2)
  AND (?3 = '' OR status = ?3)
ORDER BY caught DESC, updated_at DESC, rowid DESC
LIMIT ?4;

-- name: CreateRuleProposal :one
INSERT INTO autonomy_rule_proposals (
    id, workflow_id, label, repo_id, path_globs, task_type, min_changed_lines, rule_key, rationale,
    evidence, caught, evidence_total, cost, clean_approvals, unknown, run_id, provider_config_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: RefreshRuleProposal :one
-- A later run produced the same conditions again: refresh evidence and counts.
-- The status (and, for a dismissed proposal, that it stays dismissed) is kept.
UPDATE autonomy_rule_proposals
SET rationale = ?1, evidence = ?2, caught = ?3, evidence_total = ?4, cost = ?5, clean_approvals = ?6,
    unknown = ?7, run_id = ?8, provider_config_id = ?9, updated_at = ?10
WHERE id = ?11
RETURNING *;

-- name: DecideRuleProposal :execrows
-- Only a proposal still 'proposed' can be accepted or dismissed.
UPDATE autonomy_rule_proposals
SET status = ?1, accepted_rule_id = ?2, decided_by = ?3, decided_at = ?4, updated_at = ?4
WHERE id = ?5 AND status = 'proposed';

-- name: ListGateRejections :many
-- Human, non-forced exits from a gate since a cutoff, with the note, newest
-- first. The caller keeps the ones that went down a failure path.
SELECT h.task_id AS task_id, h.to_label AS to_label, h.created_at AS created_at,
       CAST(COALESCE(h.note, '') AS TEXT) AS note, t.title AS title
FROM task_label_history h
JOIN tasks t ON t.id = h.task_id
WHERE t.workflow_id = sqlc.arg(workflow_id)
  AND h.from_label = sqlc.arg(label)
  AND h.trigger = 'human'
  AND (h.note IS NULL OR h.note NOT LIKE 'Forced move%')
  AND h.created_at >= CAST(sqlc.arg(since) AS TEXT)
ORDER BY h.created_at DESC, h.rowid DESC
LIMIT sqlc.arg(max_rows);
