-- Queries for task_source_comments: the append-only, deduped comment thread
-- ingested from a task's source item (e.g. its GitHub issue). See
-- 044_issue_sync_updates for the table. Kept in its own file, like
-- review_comments.sql / pr_review_state.sql for their tables, rather than
-- folded into tasks.sql.

-- name: CreateTaskSourceComment :one
-- Ingests one comment from a task's source item's comment thread. Dedup is
-- via GetTaskSourceCommentByExternalID before calling this, backed by the
-- (task_id, external_id) unique index.
INSERT INTO task_source_comments (id, task_id, external_id, author, body, external_created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListTaskSourceComments :many
SELECT * FROM task_source_comments WHERE task_id = ? ORDER BY external_created_at ASC, id ASC;

-- name: GetTaskSourceCommentByExternalID :one
SELECT * FROM task_source_comments WHERE task_id = ? AND external_id = ?;
