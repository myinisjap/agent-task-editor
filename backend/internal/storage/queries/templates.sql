-- name: ListTaskTemplates :many
SELECT id, name, title, description, type, created_at, updated_at FROM task_templates ORDER BY name;

-- name: GetTaskTemplate :one
SELECT id, name, title, description, type, created_at, updated_at FROM task_templates WHERE id = ?;

-- name: CreateTaskTemplate :one
INSERT INTO task_templates (id, name, title, description, type)
VALUES (?, ?, ?, ?, ?)
RETURNING id, name, title, description, type, created_at, updated_at;

-- name: UpdateTaskTemplate :one
UPDATE task_templates
SET name = ?, title = ?, description = ?, type = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, name, title, description, type, created_at, updated_at;

-- name: DeleteTaskTemplate :exec
DELETE FROM task_templates WHERE id = ?;
