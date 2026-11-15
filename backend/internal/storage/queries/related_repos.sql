-- name: AddTaskRelatedRepo :exec
INSERT OR IGNORE INTO task_related_repos (task_id, repo_id) VALUES (?, ?);

-- name: ListTaskRelatedRepos :many
SELECT repo_id FROM task_related_repos WHERE task_id = ? ORDER BY created_at, repo_id;

-- name: TaskRelatedRepoExists :one
SELECT COUNT(*) FROM task_related_repos WHERE task_id = ? AND repo_id = ?;
