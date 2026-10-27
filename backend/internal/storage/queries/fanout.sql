-- name: UpsertTaskFileScope :exec
INSERT INTO task_fanout (task_id, file_scope) VALUES (?, ?)
ON CONFLICT(task_id) DO UPDATE SET file_scope = excluded.file_scope;

-- name: SetTaskIntegrationStatus :exec
INSERT INTO task_fanout (task_id, integration_status) VALUES (?, ?)
ON CONFLICT(task_id) DO UPDATE SET integration_status = excluded.integration_status;

-- name: GetTaskFanout :one
SELECT task_id, file_scope, integration_status FROM task_fanout WHERE task_id = ?;

-- name: ListTaskFanoutForTasks :many
SELECT task_id, file_scope, integration_status FROM task_fanout WHERE task_id IN (sqlc.slice('task_ids'));
