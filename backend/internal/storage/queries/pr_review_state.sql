-- name: GetTaskPRReviewState :one
SELECT * FROM task_pr_review_state WHERE task_id = ?;

-- name: UpsertTaskPRReviewState :one
INSERT INTO task_pr_review_state (task_id, head_sha, last_review_submitted_at, last_failed_check_sha, updated_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(task_id) DO UPDATE SET
    head_sha = excluded.head_sha,
    last_review_submitted_at = excluded.last_review_submitted_at,
    last_failed_check_sha = excluded.last_failed_check_sha,
    updated_at = CURRENT_TIMESTAMP
RETURNING *;
