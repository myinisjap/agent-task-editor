-- Intake routing rules: match->apply table evaluated at task-creation time.
-- See migration 051 and internal/intake for the matcher.

-- name: ListIntakeRules :many
SELECT id, name, enabled, sort_order, match_source, match_repo_id, match_labels, match_title_pattern, match_body_pattern, match_author_assoc, apply_template_id, apply_priority, apply_target_label, apply_workflow_id, apply_max_cost_usd, stop_processing, created_at, updated_at
FROM intake_rules ORDER BY sort_order, created_at;

-- name: ListEnabledIntakeRules :many
-- The candidate set for a match sweep: only enabled rules, walked in
-- sort_order for first-match-wins evaluation (see internal/intake.Match).
SELECT id, name, enabled, sort_order, match_source, match_repo_id, match_labels, match_title_pattern, match_body_pattern, match_author_assoc, apply_template_id, apply_priority, apply_target_label, apply_workflow_id, apply_max_cost_usd, stop_processing, created_at, updated_at
FROM intake_rules WHERE enabled != 0 ORDER BY sort_order, created_at;

-- name: GetIntakeRule :one
SELECT id, name, enabled, sort_order, match_source, match_repo_id, match_labels, match_title_pattern, match_body_pattern, match_author_assoc, apply_template_id, apply_priority, apply_target_label, apply_workflow_id, apply_max_cost_usd, stop_processing, created_at, updated_at
FROM intake_rules WHERE id = ?;

-- name: CreateIntakeRule :one
INSERT INTO intake_rules (id, name, enabled, sort_order, match_source, match_repo_id, match_labels, match_title_pattern, match_body_pattern, match_author_assoc, apply_template_id, apply_priority, apply_target_label, apply_workflow_id, apply_max_cost_usd)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, name, enabled, sort_order, match_source, match_repo_id, match_labels, match_title_pattern, match_body_pattern, match_author_assoc, apply_template_id, apply_priority, apply_target_label, apply_workflow_id, apply_max_cost_usd, stop_processing, created_at, updated_at;

-- name: UpdateIntakeRule :one
UPDATE intake_rules
SET name = ?, enabled = ?, sort_order = ?, match_source = ?, match_repo_id = ?, match_labels = ?, match_title_pattern = ?, match_body_pattern = ?, match_author_assoc = ?, apply_template_id = ?, apply_priority = ?, apply_target_label = ?, apply_workflow_id = ?, apply_max_cost_usd = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, name, enabled, sort_order, match_source, match_repo_id, match_labels, match_title_pattern, match_body_pattern, match_author_assoc, apply_template_id, apply_priority, apply_target_label, apply_workflow_id, apply_max_cost_usd, stop_processing, created_at, updated_at;

-- name: DeleteIntakeRule :exec
DELETE FROM intake_rules WHERE id = ?;

-- name: CountReposWithIssueSyncLabel :one
-- Used at startup/sweep to warn (at most once) that issue_sync_label is
-- deprecated in favor of intake rules — see docs/task-sources.md.
SELECT COUNT(*) FROM repos WHERE issue_sync_enabled != 0 AND issue_sync_label != '';
