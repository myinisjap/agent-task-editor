-- name: ListOutcomeQualityTasks :many
-- One row per non-archived task with the fields needed to drive the
-- outcome-quality aggregates in Go (rework rate, cost-to-done, review
-- burden, human-touch rate, escalation rate - see
-- handlers.OutcomeQualityHandler): the task's repo (for the optional repo
-- filter) and whether its current label is terminal (is_terminal != 0).
-- Deliberately unfiltered by label/status - the caller decides which tasks
-- are "done" via is_terminal, since a task can leave and re-enter a
-- terminal-ish label more than once across its history.
SELECT t.id AS task_id,
       t.repo_id AS repo_id,
       t.archived AS archived,
       CAST(COALESCE(wl.is_terminal, 0) AS INTEGER) AS is_terminal
FROM tasks t
LEFT JOIN workflow_labels wl ON wl.workflow_id = t.workflow_id AND wl.name = t.label;

-- name: ListOutcomeQualityLabelHistory :many
-- Every task_label_history row across all tasks, ordered so a single
-- sequential pass in Go can walk each task's label sequence in order (used
-- to compute rework: a transition into a label the task has already
-- occupied) and detect human-triggered transitions (human-touch rate).
-- Unbounded by design like the rest of this query set - see
-- OutcomeQualityHandler's doc comment for the caching strategy that keeps
-- this affordable as the table grows.
SELECT id, task_id, from_label, to_label, trigger, created_at
FROM task_label_history
ORDER BY task_id, created_at ASC, id ASC;

-- name: ListOutcomeQualityRuns :many
-- Every agent_runs row across all tasks, ordered so a single sequential
-- pass in Go can attribute a task's history to whichever agent config's run
-- most recently preceded it (see ListOutcomeQualityLabelHistory), and sum
-- cost-to-done across every run of a task regardless of status - same
-- "every run counts" rationale as SumTaskCost/SumUsageByTask above. Runs
-- whose agent_config was later deleted (agent_config_id NULL) are still
-- included for cost purposes but can't be attributed to a config.
SELECT ar.id AS run_id,
       ar.task_id AS task_id,
       ar.agent_config_id AS agent_config_id,
       ar.status AS status,
       CAST(ar.cost_usd AS REAL) AS cost_usd,
       ar.created_at AS created_at
FROM agent_runs ar
ORDER BY ar.task_id, ar.created_at ASC, ar.id ASC;

-- name: ListOutcomeQualityAgentConfigs :many
-- Agent config id -> (name, provider) lookup for the outcome-quality
-- endpoint, including configs with zero runs so a brand-new config still
-- shows a (zeroed) row rather than being silently absent.
SELECT ac.id AS agent_config_id, ac.name AS agent_name, pc.provider AS provider
FROM agent_configs ac
JOIN provider_configs pc ON pc.id = ac.provider_config_id;

-- name: CountReviewCommentsByTask :many
-- Per-task review-comment counts (all statuses, open and resolved), used to
-- compute review burden (comments received per task) attributed the same
-- way as rework/cost - see ListOutcomeQualityRuns.
SELECT task_id, COUNT(*) AS comment_count
FROM task_review_comments
GROUP BY task_id;
