-- name: ListAgentRuns :many
SELECT * FROM agent_runs WHERE task_id = ? ORDER BY created_at DESC;

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
-- config, used to resume the session on the next run (claude provider).
-- Positional params: ?1 task_id, ?2 agent_config_id.
SELECT session_id FROM agent_runs
WHERE task_id = ?1 AND agent_config_id = ?2 AND session_id != ''
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: SetAgentRunCompleted :one
UPDATE agent_runs
SET status = ?, stored_info = ?, notes = ?, input_tokens = ?, output_tokens = ?, cost_usd = ?, completed_at = CURRENT_TIMESTAMP
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

-- name: CreateAgentLog :exec
INSERT INTO agent_logs (id, agent_run_id, timestamp, type, content)
VALUES (?, ?, ?, ?, ?);

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
SELECT ar.*, t.title as task_title
FROM agent_runs ar
JOIN tasks t ON t.id = ar.task_id
WHERE ar.status = 'waiting_human'
ORDER BY ar.created_at DESC;

-- name: SumUsageTotal :one
SELECT CAST(COALESCE(SUM(input_tokens),0) AS INTEGER) AS input_tokens,
       CAST(COALESCE(SUM(output_tokens),0) AS INTEGER) AS output_tokens,
       CAST(COALESCE(SUM(cost_usd),0) AS REAL) AS cost_usd
FROM agent_runs
WHERE status IN ('completed','failed','waiting_human');

-- name: SumUsageByProvider :many
SELECT ac.provider AS provider,
       CAST(COALESCE(SUM(ar.input_tokens),0) AS INTEGER) AS input_tokens,
       CAST(COALESCE(SUM(ar.output_tokens),0) AS INTEGER) AS output_tokens,
       CAST(COALESCE(SUM(ar.cost_usd),0) AS REAL) AS cost_usd,
       COUNT(*) AS run_count
FROM agent_runs ar
JOIN agent_configs ac ON ac.id = ar.agent_config_id
WHERE ar.status IN ('completed','failed','waiting_human')
GROUP BY ac.provider
ORDER BY cost_usd DESC;

-- name: RunStatsByAgentConfig :many
-- Per-agent-config run outcome, duration, and token/cost aggregates for the
-- dashboard's per-agent-config analytics table. Only runs in a terminal
-- status (completed/failed/waiting_human) with a still-existing agent_config
-- (agent_config_id IS NOT NULL - it's set NULL on config delete, see
-- agent_runs_new migration) are included, matching SumUsageByProvider's
-- filtering above. Duration is only averaged over rows that actually have
-- both started_at and completed_at (e.g. a run that failed before starting
-- has neither and would otherwise skew the average toward zero).
SELECT ac.id AS agent_config_id,
       ac.name AS agent_name,
       ac.provider AS provider,
       COUNT(*) AS run_count,
       CAST(COALESCE(SUM(CASE WHEN ar.status = 'completed' THEN 1 ELSE 0 END),0) AS INTEGER) AS completed_count,
       CAST(COALESCE(SUM(CASE WHEN ar.status = 'failed' THEN 1 ELSE 0 END),0) AS INTEGER) AS failed_count,
       CAST(COALESCE(SUM(CASE WHEN ar.status = 'waiting_human' THEN 1 ELSE 0 END),0) AS INTEGER) AS waiting_human_count,
       CAST(COALESCE(AVG(CASE WHEN ar.started_at IS NOT NULL AND ar.completed_at IS NOT NULL
                THEN (julianday(ar.completed_at) - julianday(ar.started_at)) * 86400.0
                ELSE NULL END), 0) AS REAL) AS avg_duration_secs,
       CAST(COALESCE(SUM(ar.input_tokens),0) AS INTEGER) AS input_tokens,
       CAST(COALESCE(SUM(ar.output_tokens),0) AS INTEGER) AS output_tokens,
       CAST(COALESCE(SUM(ar.cost_usd),0) AS REAL) AS cost_usd
FROM agent_runs ar
JOIN agent_configs ac ON ac.id = ar.agent_config_id
WHERE ar.status IN ('completed','failed','waiting_human')
GROUP BY ac.id, ac.name, ac.provider
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

-- name: ListTaskLastAgentConfig :many
-- For every task sitting on a terminal label, returns the agent_config_id of
-- its *last* run (by created_at/id, the same tiebreak used elsewhere), the
-- number of runs that task had under a still-existing agent_config (used to
-- compute "turns to done" per config), and the task's current
-- transient_retry_count. Note this is a live snapshot of
-- tasks.transient_retry_count, which resets to 0 on success or escalation -
-- it is NOT a lifetime/historical retry count. Turns-to-done and the retry
-- snapshot are both attributed entirely to the task's last agent config, not
-- proportionally split across every config the task passed through.
SELECT t.id AS task_id,
       t.transient_retry_count AS transient_retry_count,
       (
         SELECT ar.agent_config_id FROM agent_runs ar
         WHERE ar.task_id = t.id AND ar.agent_config_id IS NOT NULL
         ORDER BY ar.created_at DESC, ar.id DESC
         LIMIT 1
       ) AS last_agent_config_id,
       (
         SELECT COUNT(*) FROM agent_runs ar
         WHERE ar.task_id = t.id AND ar.agent_config_id IS NOT NULL
       ) AS run_count
FROM tasks t
JOIN workflow_labels wl ON wl.workflow_id = t.workflow_id AND wl.name = t.label
WHERE wl.is_terminal != 0;
