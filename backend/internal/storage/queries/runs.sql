-- name: ListAgentRuns :many
SELECT * FROM agent_runs WHERE task_id = ? ORDER BY created_at DESC;

-- name: ListAgentRunsPage :many
-- Cursor-paginated agent-run listing for a task, newest first. Positional
-- params (?1 task_id, ?2 after cursor = created_at then id of last row, ?3
-- limit) sidestep the sqlc SQLite byte-offset bug; keep this comment
-- ASCII-only. Ordering is (created_at, id) descending so the cursor is a
-- stable total order, matching SearchTasksPage/ListAgentLogsPage.
SELECT r.* FROM agent_runs r
WHERE r.task_id = ?1
  AND (
    ?2 = ''
    OR r.created_at < (SELECT created_at FROM agent_runs WHERE id = ?2)
    OR (r.created_at = (SELECT created_at FROM agent_runs WHERE id = ?2) AND r.id < ?2)
  )
ORDER BY r.created_at DESC, r.id DESC
LIMIT ?3;

-- name: GetAgentRun :one
SELECT * FROM agent_runs WHERE id = ?;

-- name: CreateAgentRun :one
INSERT INTO agent_runs (id, task_id, agent_config_id, status, feedback)
VALUES (?, ?, ?, 'pending', ?)
RETURNING *;

-- name: SetAgentRunFeedback :exec
UPDATE agent_runs SET feedback = ? WHERE id = ?;

-- name: UpdateAgentRunStatus :one
UPDATE agent_runs
SET status = ?
WHERE id = ?
RETURNING *;

-- name: SetAgentRunStarted :one
UPDATE agent_runs
SET status = 'running', started_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: SetAgentRunSession :exec
UPDATE agent_runs SET session_id = ? WHERE id = ?;

-- name: GetLatestTaskSession :one
-- Latest non-empty provider session recorded for this task under this agent
-- config, used to resume the session on the next run. Honored today for
-- claude, qwen_code, codex_cli, and opencode (see
-- agent.providerSupportsResume).
-- Positional params: ?1 task_id, ?2 agent_config_id.
SELECT session_id FROM agent_runs
WHERE task_id = ?1 AND agent_config_id = ?2 AND session_id != ''
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: SetAgentRunCompleted :one
UPDATE agent_runs
SET status = ?, stored_info = ?, notes = ?, input_tokens = ?, output_tokens = ?, cost_usd = ?, cost_unknown = ?, turns_used = ?, completed_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: ListAgentLogs :many
SELECT * FROM agent_logs WHERE agent_run_id = ? ORDER BY timestamp ASC;

-- name: ListAgentLogsPage :many
-- Cursor-paginated log fetch, newest first. Returns the most recent entries
-- (up to the limit) for a run that are older than the cursor (the id of the
-- oldest entry the caller already has). An empty cursor returns the newest
-- entries (the tail). Callers reverse the slice for chronological display and
-- use the oldest returned id as the next cursor to "load earlier". Ordering is
-- (timestamp, id) descending with id as a stable tiebreaker; the cursor
-- comparison reads the anchor row's own timestamp so it matches the ORDER BY
-- regardless of timestamp text format. Positional params (?1 run_id, ?2 before
-- cursor, ?3 limit) are used instead of @named ones to sidestep a byte-offset
-- bug in sqlc's SQLite analyzer that corrupts long named-parameter queries.
-- (Related: the doc comments on RunStatsByAgentConfig, ListRunDurationsBy-
-- AgentConfig, and ListTaskLastAgentConfig below intentionally use plain
-- ASCII hyphens instead of em-dashes, because a multi-byte UTF-8 character
-- anywhere in one of those comments hits the same byte-offset-vs-rune
-- assumption and truncates/corrupts the generated SQL string constant in
-- internal/storage/gen/runs.sql.go - verified by re-running `sqlc generate`
-- after switching those three to ASCII-only dashes.)
SELECT l.* FROM agent_logs l
WHERE l.agent_run_id = ?1
  AND (
    ?2 = ''
    OR l.timestamp < (SELECT timestamp FROM agent_logs WHERE id = ?2)
    OR (l.timestamp = (SELECT timestamp FROM agent_logs WHERE id = ?2) AND l.id < ?2)
  )
ORDER BY l.timestamp DESC, l.id DESC
LIMIT ?3;

-- name: CountAgentLogs :one
SELECT COUNT(*) FROM agent_logs WHERE agent_run_id = ?;

-- name: CountAgentLogsTotal :one
-- Total row count across all runs, surfaced on the Health page alongside DB
-- file size so agent_logs bloat is observable before it becomes a problem.
SELECT COUNT(*) FROM agent_logs;

-- name: CreateAgentLog :exec
INSERT INTO agent_logs (id, agent_run_id, timestamp, type, content)
VALUES (?, ?, ?, ?, ?);

-- name: DeleteOldAgentLogs :execrows
-- Deletes agent_logs rows belonging to runs in a terminal status
-- (completed/failed/waiting_human) whose run completed_at is older than the
-- cutoff. Never touches logs for a run that is still pending/running (no
-- completed_at, or non-terminal status), so the active run and the WS
-- replay path (which reads the live run's logs) are unaffected. cutoff is
-- a DATETIME-comparable string (matching SQLite's CURRENT_TIMESTAMP text
-- format) computed by the caller from LOG_RETENTION_DAYS so the predicate
-- stays testable without relying on SQLite's date('now', ...).
DELETE FROM agent_logs
WHERE agent_run_id IN (
  SELECT id FROM agent_runs
  WHERE status IN ('completed','failed','waiting_human')
    AND completed_at IS NOT NULL
    AND completed_at < ?1
);

-- name: CreateTaskLabelHistory :exec
INSERT INTO task_label_history (id, task_id, from_label, to_label, trigger, actor_id, note)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListTaskLabelHistory :many
SELECT * FROM task_label_history WHERE task_id = ? ORDER BY created_at ASC;

-- name: ListActiveAgentRuns :many
SELECT ar.*, t.title as task_title, ac.name as agent_name
FROM agent_runs ar
JOIN tasks t ON t.id = ar.task_id
JOIN agent_configs ac ON ac.id = ar.agent_config_id
WHERE ar.status = 'running'
ORDER BY ar.started_at DESC;

-- name: ListWaitingHumanRuns :many
-- Only surfaces a waiting_human run while it is still the task's active run.
-- A reply/approve/reject on a waiting_human run dispatches a new run and
-- repoints tasks.active_agent_run_id at it, but deliberately leaves the old
-- run's status as 'waiting_human' as a historical record (see ReplyRun's doc
-- comment in task_runs.go); without this join, that superseded run would
-- keep showing up in the dashboard's "needs your input" queue forever, even
-- after a new run for the same task is already active/running.
SELECT ar.*, t.title as task_title
FROM agent_runs ar
JOIN tasks t ON t.id = ar.task_id
WHERE ar.status = 'waiting_human'
  AND t.active_agent_run_id = ar.id
ORDER BY ar.created_at DESC;

-- name: SumTaskCost :one
-- Cumulative recorded cost for a task, across ALL runs regardless of status
-- (unlike SumUsageTotal/SumUsageByProvider/RunStatsByAgentConfig below,
-- which only count terminal-status runs). A cost budget must count spend
-- from every run that ran, including ones that failed or are mid-flight,
-- so a failing-then-retrying task cannot dodge its budget by never
-- reaching a terminal status. Used by the dispatcher's pre-dispatch
-- budget guard (see dispatch.go).
SELECT CAST(COALESCE(SUM(cost_usd),0) AS REAL) AS cost_usd
FROM agent_runs WHERE task_id = ?;

-- name: SumUsageByDay :many
-- Daily token/cost/run-count rollup for the dashboard's cost-by-day table,
-- most recent day first, capped at the last 30 days with recorded activity.
SELECT date(ar.completed_at) AS day,
       CAST(COALESCE(SUM(ar.input_tokens),0) AS INTEGER) AS input_tokens,
       CAST(COALESCE(SUM(ar.output_tokens),0) AS INTEGER) AS output_tokens,
       CAST(COALESCE(SUM(ar.cost_usd),0) AS REAL) AS cost_usd,
       COUNT(*) AS run_count
FROM agent_runs ar
WHERE ar.status IN ('completed','failed','waiting_human') AND ar.completed_at IS NOT NULL
GROUP BY date(ar.completed_at)
ORDER BY day DESC
LIMIT 30;

-- name: SumUsageByTask :many
-- Per-task token/cost rollup, across ALL runs regardless of status (see
-- SumTaskCost above for why - a task's "cost so far" should count every
-- run, not just terminal ones). Ordered by cost descending so the caller
-- can cheaply take a top-N slice for a "top tasks by cost" view.
SELECT ar.task_id AS task_id,
       CAST(COALESCE(SUM(ar.input_tokens),0) AS INTEGER) AS input_tokens,
       CAST(COALESCE(SUM(ar.output_tokens),0) AS INTEGER) AS output_tokens,
       CAST(COALESCE(SUM(ar.cost_usd),0) AS REAL) AS cost_usd
FROM agent_runs ar
GROUP BY ar.task_id
ORDER BY cost_usd DESC;

-- name: SumUsageTotal :one
SELECT CAST(COALESCE(SUM(input_tokens),0) AS INTEGER) AS input_tokens,
       CAST(COALESCE(SUM(output_tokens),0) AS INTEGER) AS output_tokens,
       CAST(COALESCE(SUM(cost_usd),0) AS REAL) AS cost_usd
FROM agent_runs
WHERE status IN ('completed','failed','waiting_human');

-- name: SumUsageByProvider :many
SELECT pc.provider AS provider,
       CAST(COALESCE(SUM(ar.input_tokens),0) AS INTEGER) AS input_tokens,
       CAST(COALESCE(SUM(ar.output_tokens),0) AS INTEGER) AS output_tokens,
       CAST(COALESCE(SUM(ar.cost_usd),0) AS REAL) AS cost_usd,
       COUNT(*) AS run_count
FROM agent_runs ar
JOIN agent_configs ac ON ac.id = ar.agent_config_id
JOIN provider_configs pc ON pc.id = ac.provider_config_id
WHERE ar.status IN ('completed','failed','waiting_human')
GROUP BY pc.provider
ORDER BY cost_usd DESC;

-- name: RunStatsByAgentConfig :many
-- Per-agent-config run outcome, duration, and token/cost aggregates for the
-- dashboard's per-agent-config analytics table. Only runs in a terminal
-- status (completed/failed/waiting_human) with a still-existing agent_config
-- (agent_config_id IS NOT NULL - it's set NULL on config delete, see
-- agent_runs_new migration) are included, matching SumUsageByProvider's
-- filtering above. Duration is only averaged over rows that actually have
-- both started_at and completed_at (e.g. a run that failed before starting
-- has neither and would otherwise skew the average toward zero). Likewise
-- avg_turns_used averages only over runs that actually reported a turn count
-- (turns_used > 0, via NULLIF): 0 means "not reported" for providers that
-- expose no count, and averaging those in would understate the real figure.
-- max_turns is the config's currently-configured cap, returned alongside so
-- the dashboard can show "avg used vs. cap" without a second lookup; it is a
-- live value, not the cap in force when the historical runs executed.
SELECT ac.id AS agent_config_id,
       ac.name AS agent_name,
       pc.provider AS provider,
       COUNT(*) AS run_count,
       CAST(COALESCE(SUM(CASE WHEN ar.status = 'completed' THEN 1 ELSE 0 END),0) AS INTEGER) AS completed_count,
       CAST(COALESCE(SUM(CASE WHEN ar.status = 'failed' THEN 1 ELSE 0 END),0) AS INTEGER) AS failed_count,
       CAST(COALESCE(SUM(CASE WHEN ar.status = 'waiting_human' THEN 1 ELSE 0 END),0) AS INTEGER) AS waiting_human_count,
       CAST(COALESCE(AVG(CASE WHEN ar.started_at IS NOT NULL AND ar.completed_at IS NOT NULL
                THEN (julianday(ar.completed_at) - julianday(ar.started_at)) * 86400.0
                ELSE NULL END), 0) AS REAL) AS avg_duration_secs,
       CAST(COALESCE(SUM(ar.input_tokens),0) AS INTEGER) AS input_tokens,
       CAST(COALESCE(SUM(ar.output_tokens),0) AS INTEGER) AS output_tokens,
       CAST(COALESCE(SUM(ar.cost_usd),0) AS REAL) AS cost_usd,
       CAST(COALESCE(AVG(NULLIF(ar.turns_used, 0)), 0) AS REAL) AS avg_turns_used,
       ac.max_turns AS max_turns
FROM agent_runs ar
JOIN agent_configs ac ON ac.id = ar.agent_config_id
JOIN provider_configs pc ON pc.id = ac.provider_config_id
WHERE ar.status IN ('completed','failed','waiting_human')
GROUP BY ac.id, ac.name, pc.provider, ac.max_turns
ORDER BY run_count DESC;

-- name: ListRunDurationsByAgentConfig :many
-- Raw per-run duration (seconds) for terminal-state runs with a
-- still-existing agent_config, ordered by agent_config then duration
-- ascending so the caller can slice out a p90 per group in Go (SQLite has no
-- built-in percentile aggregate). Only rows with both started_at and
-- completed_at set are included - see RunStatsByAgentConfig for why.
SELECT ar.agent_config_id AS agent_config_id,
       CAST((julianday(ar.completed_at) - julianday(ar.started_at)) * 86400.0 AS REAL) AS duration_secs
FROM agent_runs ar
WHERE ar.status IN ('completed','failed','waiting_human')
  AND ar.agent_config_id IS NOT NULL
  AND ar.started_at IS NOT NULL
  AND ar.completed_at IS NOT NULL
ORDER BY ar.agent_config_id, duration_secs ASC;

-- name: ListRunTurnsByAgentConfig :many
-- Raw per-run turn count for terminal-state runs with a still-existing
-- agent_config, ordered by agent_config then turns ascending so the caller
-- can slice out a p90 per group in Go (same shape and rationale as
-- ListRunDurationsByAgentConfig above). Runs that reported no turn count
-- (turns_used = 0) are excluded rather than counted as zero-turn runs - see
-- RunStatsByAgentConfig for why.
SELECT ar.agent_config_id AS agent_config_id,
       ar.turns_used AS turns_used
FROM agent_runs ar
WHERE ar.status IN ('completed','failed','waiting_human')
  AND ar.agent_config_id IS NOT NULL
  AND ar.turns_used > 0
ORDER BY ar.agent_config_id, turns_used ASC;

-- name: ListTaskLastAgentConfig :many
-- For every task sitting on a terminal label, returns the agent_config_id of
-- its *last* run (by created_at/id, the same tiebreak used elsewhere) and the
-- task's current transient_retry_count. Note this is a live snapshot of
-- tasks.transient_retry_count, which resets to 0 on success or escalation -
-- it is NOT a lifetime/historical retry count. The retry snapshot is
-- attributed entirely to the task's last agent config, not proportionally
-- split across every config the task passed through (unlike avg_runs_per_task
-- below, which is a proportional split - see ListTaskRunCountsByAgentConfig).
SELECT t.id AS task_id,
       t.transient_retry_count AS transient_retry_count,
       (
         SELECT ar.agent_config_id FROM agent_runs ar
         WHERE ar.task_id = t.id AND ar.agent_config_id IS NOT NULL
         ORDER BY ar.created_at DESC, ar.id DESC
         LIMIT 1
       ) AS last_agent_config_id
FROM tasks t
JOIN workflow_labels wl ON wl.workflow_id = t.workflow_id AND wl.name = t.label
WHERE wl.is_terminal != 0;

-- name: ListTaskRunCountsByAgentConfig :many
-- For every task sitting on a terminal label, and for each agent_config that
-- contributed at least one run to that task, returns how many runs that
-- config contributed. Used to compute avg_runs_per_task per config: each
-- done task splits 1.0 "task credit" proportionally across every config that
-- worked on it (weighted by that config's share of the task's total runs),
-- rather than crediting the whole task to only its last-run config.
SELECT t.id AS task_id,
       ar.agent_config_id AS agent_config_id,
       COUNT(*) AS run_count
FROM tasks t
JOIN workflow_labels wl ON wl.workflow_id = t.workflow_id AND wl.name = t.label
JOIN agent_runs ar ON ar.task_id = t.id AND ar.agent_config_id IS NOT NULL
WHERE wl.is_terminal != 0
GROUP BY t.id, ar.agent_config_id;

-- name: SumCostForDay :one
-- Cumulative recorded cost across ALL runs regardless of status (same "every
-- run counts" rationale as SumTaskCost above), for a single UTC calendar day.
-- Used by the dispatcher's global daily spend-ceiling guard (see
-- Dispatcher.checkGlobalCostBudget) so a rate-limited/failed/in-flight run
-- that already burned spend still counts against the cap the same sweep it
-- happened, not just once it reaches a terminal status. day is a
-- 'YYYY-MM-DD' string (UTC), matching date(created_at) below; the
-- CAST(sqlc.arg(day) AS TEXT) pins the generated Go param type to string
-- instead of sqlc inferring time.Time from the datetime column it's
-- compared against.
SELECT CAST(COALESCE(SUM(cost_usd),0) AS REAL) AS cost_usd
FROM agent_runs WHERE date(created_at) = CAST(sqlc.arg(day) AS TEXT);

-- name: SumCostForMonth :one
-- Cumulative recorded cost across ALL runs regardless of status, for a
-- single UTC calendar month. month is a 'YYYY-MM' string (UTC), matching
-- strftime('%Y-%m', created_at) below; the CAST(sqlc.arg(month) AS TEXT)
-- pins the generated Go param type to string, same reason as SumCostForDay
-- above. See SumCostForDay for the "every run counts" rationale and its
-- dispatcher usage.
SELECT CAST(COALESCE(SUM(cost_usd),0) AS REAL) AS cost_usd
FROM agent_runs WHERE strftime('%Y-%m', created_at) = CAST(sqlc.arg(month) AS TEXT);

-- name: SumUsageByRepo :many
-- Per-repo token/cost rollup across ALL runs regardless of status (same
-- rationale as SumUsageByTask above), joined through tasks since agent_runs
-- has no repo_id of its own. Ordered by cost descending so the caller can
-- cheaply take a top-N slice for a "which repo is expensive" view -- the
-- natural companion to repos.max_concurrent_runs for an operator setting
-- per-repo limits.
SELECT t.repo_id AS repo_id,
       CAST(COALESCE(SUM(ar.input_tokens),0) AS INTEGER) AS input_tokens,
       CAST(COALESCE(SUM(ar.output_tokens),0) AS INTEGER) AS output_tokens,
       CAST(COALESCE(SUM(ar.cost_usd),0) AS REAL) AS cost_usd,
       COUNT(*) AS run_count
FROM agent_runs ar
JOIN tasks t ON t.id = ar.task_id
GROUP BY t.repo_id
ORDER BY cost_usd DESC;

-- name: CountTaskCostUnknownRuns :one
-- Count of a task's agent_runs rows (across ALL statuses, same "every run
-- counts" rationale as SumTaskCost above) flagged cost_unknown = 1, i.e.
-- at least one token was consumed but no price could be resolved for the
-- configured model (see agent.Result.CostUnknown / providers.PriceResolver).
-- Used by the dispatcher's pre-dispatch budget guard to detect when
-- SumTaskCost's total can't be trusted as "true accumulated spend": a task
-- could sit well under a nonzero budget purely because one or more of its
-- runs recorded cost_usd = 0 for an unpriced model rather than a genuinely
-- free run.
--
-- NOTE: this comment must stay ASCII-only (no em dashes/smart quotes) --
-- sqlc v1.31.1's sqlite tokenizer mis-locates the query's final token when
-- a non-ASCII byte appears in a preceding "--" comment, silently truncating
-- the generated query string (confirmed by bisection while adding this
-- query; every other .sql file in this directory is ASCII-only for the
-- same likely reason).
SELECT CAST(COUNT(*) AS INTEGER) AS unknown_count
FROM agent_runs WHERE task_id = ? AND cost_unknown != 0;
