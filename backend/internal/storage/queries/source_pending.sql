-- name: UpsertTaskSourcePending :exec
-- Re-upserting identical text keeps the dismissed flag; different text resets it.
INSERT INTO task_source_pending (task_id, title, description, type)
VALUES (?, ?, ?, ?)
ON CONFLICT (task_id) DO UPDATE
SET dismissed = CASE WHEN title = excluded.title AND description = excluded.description AND type = excluded.type THEN dismissed ELSE 0 END,
    title = excluded.title, description = excluded.description, type = excluded.type, updated_at = CURRENT_TIMESTAMP;

-- name: GetTaskSourcePending :one
SELECT task_id, title, description, type, dismissed, created_at, updated_at
FROM task_source_pending WHERE task_id = ?;

-- name: ListTaskSourcePendingForTasks :many
SELECT task_id, title, description, type, dismissed, created_at, updated_at
FROM task_source_pending WHERE dismissed = 0 AND task_id IN (sqlc.slice('ids'));

-- name: DismissTaskSourcePending :execrows
UPDATE task_source_pending SET dismissed = 1, updated_at = CURRENT_TIMESTAMP
WHERE task_id = ? AND dismissed = 0;

-- name: DeleteTaskSourcePending :execrows
DELETE FROM task_source_pending WHERE task_id = ?;
