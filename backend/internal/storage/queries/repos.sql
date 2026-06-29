-- name: ListRepos :many
SELECT * FROM repos ORDER BY created_at DESC;

-- name: GetRepo :one
SELECT * FROM repos WHERE id = ?;

-- name: CreateRepo :one
INSERT INTO repos (id, name, path, remote_url, workflow_id)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateRepo :one
UPDATE repos
SET name = ?, path = ?, remote_url = ?, workflow_id = ?
WHERE id = ?
RETURNING *;

-- name: DeleteRepo :exec
DELETE FROM repos WHERE id = ?;
