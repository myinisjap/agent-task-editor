-- name: CreateChatSession :one
INSERT INTO chat_sessions (id, repo_id, provider, model, title)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetChatSession :one
SELECT * FROM chat_sessions WHERE id = ?;

-- name: ListChatSessions :many
SELECT * FROM chat_sessions ORDER BY updated_at DESC;

-- name: SetChatSessionWorktree :exec
UPDATE chat_sessions SET worktree_path = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: SetChatSessionTitle :exec
UPDATE chat_sessions SET title = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: DeleteChatSession :exec
DELETE FROM chat_sessions WHERE id = ?;
