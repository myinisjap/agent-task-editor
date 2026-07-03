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

-- name: SetAgentRunCompleted :one
UPDATE agent_runs
SET status = ?, stored_info = ?, notes = ?, input_tokens = ?, output_tokens = ?, cost_usd = ?, completed_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: ListAgentLogs :many
SELECT * FROM agent_logs WHERE agent_run_id = ? ORDER BY timestamp ASC;

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
