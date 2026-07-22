-- name: ListRepos :many
SELECT * FROM repos ORDER BY created_at DESC;

-- name: GetRepo :one
SELECT * FROM repos WHERE id = ?;

-- name: ListIssueSyncRepos :many
SELECT * FROM repos WHERE issue_sync_enabled != 0 ORDER BY created_at DESC;

-- name: CreateRepo :one
INSERT INTO repos (id, name, path, remote_url, workflow_id, issue_sync_enabled, issue_sync_label, issue_writeback_enabled, pr_review_auto_transition_enabled)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateRepo :one
UPDATE repos
SET name = ?, path = ?, remote_url = ?, workflow_id = ?, issue_sync_enabled = ?, issue_sync_label = ?, issue_writeback_enabled = ?, pr_review_auto_transition_enabled = ?
WHERE id = ?
RETURNING *;

-- name: SetRepoCloneStatus :exec
UPDATE repos
SET clone_status = ?, clone_error = ?
WHERE id = ?;

-- name: DeleteRepo :exec
DELETE FROM repos WHERE id = ?;
