-- name: EnqueueTaskSideEffect :exec
-- Idempotent: the partial unique index on pending rows makes a second enqueue
-- of the same kind for the same task a no-op while one is still waiting.
INSERT INTO task_side_effects (id, task_id, kind)
VALUES (?, ?, ?)
ON CONFLICT (kind, task_id) WHERE status = 'pending' DO NOTHING;

-- name: ListDueTaskSideEffects :many
SELECT id, task_id, kind, status, attempts, next_at, last_error, created_at, updated_at
FROM task_side_effects
WHERE status = 'pending' AND next_at <= ?
ORDER BY next_at ASC, created_at ASC
LIMIT ?;

-- name: ClaimTaskSideEffect :execrows
UPDATE task_side_effects
SET status = 'running', updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND status = 'pending';

-- name: CompleteTaskSideEffect :exec
DELETE FROM task_side_effects WHERE id = ?;

-- name: RetryTaskSideEffectLater :exec
UPDATE task_side_effects
SET status = 'pending', attempts = ?, next_at = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: FailTaskSideEffect :exec
UPDATE task_side_effects
SET status = 'failed', attempts = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: ResetRunningTaskSideEffects :execrows
UPDATE task_side_effects
SET status = 'pending', updated_at = CURRENT_TIMESTAMP
WHERE status = 'running';

-- name: RequeueFailedTaskSideEffects :execrows
UPDATE task_side_effects
SET status = 'pending', attempts = 0, next_at = ?, updated_at = CURRENT_TIMESTAMP
WHERE task_id = ? AND status = 'failed';

-- name: ListTaskSideEffectsForTasks :many
SELECT task_id, kind, status, attempts, next_at, last_error
FROM task_side_effects
WHERE task_id IN (sqlc.slice('task_ids'))
ORDER BY created_at ASC;
