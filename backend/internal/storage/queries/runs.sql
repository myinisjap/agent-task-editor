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
