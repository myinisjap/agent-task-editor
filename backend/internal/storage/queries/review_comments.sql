-- name: CreateTaskReviewComment :one
INSERT INTO task_review_comments (id, task_id, file_path, side, start_line, end_line, quoted_text, body)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: CreateForgeTaskReviewComment :one
-- Like CreateTaskReviewComment but for a comment ingested from a PR/MR review
-- on some forge (source = e.g. 'github' or 'gitea' -- see internal/forge),
-- tagged with the forge's comment id (external_id) so re-sweeps can dedup via
-- GetTaskReviewCommentByExternalID before inserting.
INSERT INTO task_review_comments (id, task_id, file_path, side, start_line, end_line, quoted_text, body, external_id, source)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetTaskReviewCommentByExternalID :one
SELECT * FROM task_review_comments WHERE task_id = ? AND external_id = ?;

-- name: GetTaskReviewComment :one
SELECT * FROM task_review_comments WHERE id = ? AND task_id = ?;

-- name: ListTaskReviewComments :many
SELECT * FROM task_review_comments WHERE task_id = ? ORDER BY created_at ASC;

-- name: ListOpenTaskReviewComments :many
SELECT * FROM task_review_comments WHERE task_id = ? AND status = 'open' ORDER BY created_at ASC;

-- name: ResolveTaskReviewComment :one
UPDATE task_review_comments
SET status = 'resolved', resolution_note = ?, resolved_by_run_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND task_id = ? AND status = 'open'
RETURNING *;

-- name: ReopenTaskReviewComment :one
UPDATE task_review_comments
SET status = 'open', resolution_note = NULL, resolved_by_run_id = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND task_id = ?
RETURNING *;

-- name: DeleteTaskReviewComment :execrows
DELETE FROM task_review_comments WHERE id = ? AND task_id = ?;

-- name: ListUnrepliedAgentResolvedForgeReviewComments :many
-- Forge-ingested comments an agent run resolved that have not yet had their
-- thread reply posted (reply_posted_at is the once-per-comment marker).
SELECT * FROM task_review_comments
WHERE task_id = ? AND status = 'resolved' AND resolved_by_run_id IS NOT NULL
  AND external_id IS NOT NULL AND source != 'local' AND reply_posted_at IS NULL
ORDER BY created_at ASC;

-- name: MarkTaskReviewCommentReplied :exec
UPDATE task_review_comments
SET reply_external_id = ?, reply_posted_at = CURRENT_TIMESTAMP
WHERE id = ? AND task_id = ?;

-- name: CountTaskReviewCommentReplyByExternalID :one
-- Whether a forge comment id is a reply ATE itself posted.
SELECT COUNT(*) FROM task_review_comments WHERE task_id = ? AND reply_external_id = ?;
