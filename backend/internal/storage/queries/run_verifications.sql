-- name: UpsertRunVerification :exec
INSERT INTO run_verifications (run_id, task_id, command, status, exit_code, output, truncated, duration_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(run_id) DO UPDATE SET
    command = excluded.command,
    status = excluded.status,
    exit_code = excluded.exit_code,
    output = excluded.output,
    truncated = excluded.truncated,
    duration_ms = excluded.duration_ms,
    created_at = CURRENT_TIMESTAMP;

-- name: GetRunVerification :one
SELECT * FROM run_verifications WHERE run_id = ?;

-- name: ListTaskVerifications :many
SELECT * FROM run_verifications WHERE task_id = ? ORDER BY created_at DESC, run_id DESC LIMIT ?;
