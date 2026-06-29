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
SET status = ?, stored_info = ?, notes = ?, completed_at = CURRENT_TIMESTAMP
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
