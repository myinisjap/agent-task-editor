-- name: CreateAgentExperiment :one
INSERT INTO agent_experiments (id, agent_config_id, candidate_snapshot, base_version, share_percent, note, actor_id)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAgentExperiment :one
SELECT * FROM agent_experiments WHERE id = ?;

-- name: ListAgentExperiments :many
SELECT * FROM agent_experiments WHERE agent_config_id = ? ORDER BY started_at DESC, id DESC;

-- name: GetRunningAgentExperiment :one
SELECT * FROM agent_experiments WHERE agent_config_id = ? AND status = 'running';

-- name: ListRunningAgentExperiments :many
SELECT * FROM agent_experiments WHERE status = 'running';

-- name: StopAgentExperiment :execrows
UPDATE agent_experiments SET status = 'stopped', stopped_at = CURRENT_TIMESTAMP
WHERE id = ? AND status = 'running';

-- name: PromoteAgentExperiment :execrows
UPDATE agent_experiments SET status = 'promoted', stopped_at = CURRENT_TIMESTAMP, promoted_version = ?
WHERE id = ? AND status = 'running';

-- name: InsertAgentExperimentAssignment :exec
INSERT OR IGNORE INTO agent_experiment_assignments (experiment_id, task_id, arm) VALUES (?, ?, ?);

-- name: GetAgentExperimentAssignment :one
SELECT arm FROM agent_experiment_assignments WHERE experiment_id = ? AND task_id = ?;

-- name: HasAgentRunBeforeExperiment :one
-- True when the task already had a run by this agent config created before
-- the experiment started (such tasks are not enrolled).
SELECT EXISTS(
  SELECT 1 FROM agent_runs ar
  WHERE ar.task_id = ?1 AND ar.agent_config_id = ?2
    AND ar.created_at < (SELECT e.started_at FROM agent_experiments e WHERE e.id = ?3)
) AS has_run;

-- name: CountAgentExperimentAssignments :many
SELECT arm, COUNT(*) AS task_count FROM agent_experiment_assignments WHERE experiment_id = ? GROUP BY arm;

-- name: ListAgentExperimentTasks :many
SELECT a.task_id AS task_id,
       a.arm AS arm,
       t.archived AS archived,
       CAST(COALESCE(wl.is_terminal, 0) AS INTEGER) AS is_terminal
FROM agent_experiment_assignments a
JOIN tasks t ON t.id = a.task_id
LEFT JOIN workflow_labels wl ON wl.workflow_id = t.workflow_id AND wl.name = t.label
WHERE a.experiment_id = ?;

-- name: ListAgentExperimentTaskRuns :many
SELECT ar.task_id AS task_id,
       ar.agent_config_id AS agent_config_id,
       ar.status AS status,
       CAST(ar.cost_usd AS REAL) AS cost_usd,
       ar.started_at AS started_at,
       ar.completed_at AS completed_at,
       ar.experiment_id AS experiment_id,
       ar.experiment_arm AS experiment_arm
FROM agent_runs ar
WHERE ar.task_id IN (SELECT x.task_id FROM agent_experiment_assignments x WHERE x.experiment_id = ?)
ORDER BY ar.task_id, ar.created_at ASC, ar.id ASC;

-- name: ListAgentExperimentLabelHistory :many
SELECT task_id, to_label, trigger
FROM task_label_history
WHERE task_id IN (SELECT x.task_id FROM agent_experiment_assignments x WHERE x.experiment_id = ?)
ORDER BY task_id, created_at ASC, id ASC;

-- name: BackfillExperimentCandidateVersion :exec
UPDATE agent_runs SET agent_config_version = ? WHERE experiment_id = ? AND experiment_arm = 'candidate';
