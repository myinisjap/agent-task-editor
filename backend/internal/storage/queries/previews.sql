-- name: CreatePreviewCapture :one
INSERT INTO task_preview_captures (id, task_id, run_id, status)
VALUES (?, ?, ?, 'running')
RETURNING *;

-- name: FinishPreviewCapture :exec
UPDATE task_preview_captures
SET status = ?, error = ?, finished_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: CreatePreviewImage :exec
INSERT INTO task_preview_images (id, capture_id, task_id, route, variant, width, height, path)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLatestPreviewCapture :one
SELECT * FROM task_preview_captures
WHERE task_id = ?
ORDER BY created_at DESC, rowid DESC
LIMIT 1;

-- name: ListPreviewImagesByCapture :many
SELECT * FROM task_preview_images
WHERE capture_id = ?
ORDER BY route, width DESC;

-- name: ListOldPreviewCaptures :many
SELECT o.* FROM task_preview_captures o
WHERE o.task_id = ?1
  AND o.id NOT IN (
    SELECT c.id FROM task_preview_captures c
    WHERE c.task_id = ?1
    ORDER BY c.created_at DESC, c.rowid DESC
    LIMIT ?2
  );

-- name: ListPreviewImagePathsByCapture :many
SELECT path FROM task_preview_images WHERE capture_id = ?;

-- name: DeletePreviewCapture :exec
DELETE FROM task_preview_captures WHERE id = ?;

-- name: MarkStalePreviewCapturesFailed :exec
UPDATE task_preview_captures
SET status = 'failed', error = 'interrupted by server restart', finished_at = CURRENT_TIMESTAMP
WHERE status = 'running';
