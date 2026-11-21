-- name: UpsertRunVerification :exec
INSERT INTO run_verifications (run_id, task_id, command, status, exit_code, output, truncated, duration_ms, head_sha, reused_from_run_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(run_id) DO UPDATE SET
    command = excluded.command,
    status = excluded.status,
    exit_code = excluded.exit_code,
    output = excluded.output,
    truncated = excluded.truncated,
    duration_ms = excluded.duration_ms,
    head_sha = excluded.head_sha,
    reused_from_run_id = excluded.reused_from_run_id,
    created_at = CURRENT_TIMESTAMP;

-- name: GetRunVerification :one
SELECT * FROM run_verifications WHERE run_id = ?;

-- name: GetLatestTaskVerification :one
SELECT * FROM run_verifications WHERE task_id = ? ORDER BY created_at DESC, run_id DESC LIMIT 1;

-- name: ListTaskVerifications :many
SELECT * FROM run_verifications WHERE task_id = ? ORDER BY created_at DESC, run_id DESC LIMIT ?;

-- name: ListLatestVerifyStatusForTasks :many
-- Latest verify status per task (same ordering as ListTaskVerifications). Tasks with no row are absent (= no verify configured).
SELECT v.task_id, v.status FROM run_verifications v
WHERE v.task_id IN (sqlc.slice('task_ids'))
  AND v.run_id = (SELECT v2.run_id FROM run_verifications v2 WHERE v2.task_id = v.task_id
                  ORDER BY v2.created_at DESC, v2.run_id DESC LIMIT 1);
