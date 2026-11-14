-- name: UpsertTaskAutoMerge :exec
INSERT INTO task_auto_merge (task_id, state, reason, message, head_sha, updated_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT (task_id) DO UPDATE SET
  state = excluded.state, reason = excluded.reason, message = excluded.message,
  head_sha = excluded.head_sha,
  -- keep the time the current situation began when nothing changed
  updated_at = CASE WHEN task_auto_merge.state = excluded.state AND task_auto_merge.reason = excluded.reason AND task_auto_merge.head_sha = excluded.head_sha THEN task_auto_merge.updated_at ELSE CURRENT_TIMESTAMP END;

-- name: GetTaskAutoMerge :one
SELECT task_id, state, reason, message, head_sha, updated_at FROM task_auto_merge WHERE task_id = ?;

-- name: DeleteTaskAutoMerge :execrows
DELETE FROM task_auto_merge WHERE task_id = ?;

-- name: ListTaskAutoMergeForTasks :many
SELECT task_id, state, reason, message, head_sha, updated_at
FROM task_auto_merge
WHERE task_id IN (sqlc.slice('task_ids'));

-- name: CountTaskSideEffectsOfKind :one
SELECT COUNT(*) FROM task_side_effects WHERE task_id = ? AND kind = ?;
