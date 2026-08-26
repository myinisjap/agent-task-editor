-- name: ListRepos :many
SELECT * FROM repos ORDER BY created_at DESC;

-- name: ListReposPage :many
-- Cursor-paginated repo listing, newest first. Positional params (?1 after
-- cursor = created_at then id of last row, ?2 limit) sidestep the sqlc
-- SQLite byte-offset bug; keep this comment ASCII-only. Ordering is
-- (created_at, id) descending so the cursor is a stable total order,
-- matching SearchTasksPage/ListAgentLogsPage. ListIssueSyncRepos (the
-- internal issue-sync-enabled lookup) is deliberately left unpaginated.
SELECT r.* FROM repos r
WHERE (
    ?1 = ''
    OR r.created_at < (SELECT created_at FROM repos WHERE id = ?1)
    OR (r.created_at = (SELECT created_at FROM repos WHERE id = ?1) AND r.id < ?1)
  )
ORDER BY r.created_at DESC, r.id DESC
LIMIT ?2;

-- name: GetRepo :one
SELECT * FROM repos WHERE id = ?;

-- name: ListIssueSyncRepos :many
SELECT * FROM repos WHERE issue_sync_enabled != 0 ORDER BY created_at DESC;

-- name: CreateRepo :one
INSERT INTO repos (id, name, path, remote_url, workflow_id, issue_sync_enabled, issue_sync_label, issue_writeback_enabled, pr_review_auto_transition_enabled, issue_sync_update_policy, issue_sync_gone_action, issue_sync_gone_label, issue_comment_sync_enabled, max_concurrent_runs, issue_writeback_label, runtime_image, devcontainer_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateRepo :one
UPDATE repos
SET name = ?, path = ?, remote_url = ?, workflow_id = ?, issue_sync_enabled = ?, issue_sync_label = ?, issue_writeback_enabled = ?, pr_review_auto_transition_enabled = ?, issue_sync_update_policy = ?, issue_sync_gone_action = ?, issue_sync_gone_label = ?, issue_comment_sync_enabled = ?, max_concurrent_runs = ?, issue_writeback_label = ?, runtime_image = ?, devcontainer_json = ?
WHERE id = ?
RETURNING *;

-- name: SetRepoCloneStatus :exec
UPDATE repos
SET clone_status = ?, clone_error = ?
WHERE id = ?;

-- name: DeleteRepo :exec
DELETE FROM repos WHERE id = ?;

-- name: CountTasksByRepo :one
SELECT COUNT(*) FROM tasks WHERE repo_id = ?;
