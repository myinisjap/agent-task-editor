-- Repo map storage (088_repo_map). Keep this file ASCII-only.

-- name: GetRepoMapConfig :one
SELECT * FROM repo_map_config WHERE repo_id = ?1;

-- name: UpsertRepoMapConfig :one
INSERT INTO repo_map_config (repo_id, enabled, provider_config_id, refresh_interval_hours, min_commits_behind, updated_at)
VALUES (?1, ?2, ?3, ?4, ?5, CURRENT_TIMESTAMP)
ON CONFLICT (repo_id) DO UPDATE SET
    enabled = excluded.enabled,
    provider_config_id = excluded.provider_config_id,
    refresh_interval_hours = excluded.refresh_interval_hours,
    min_commits_behind = excluded.min_commits_behind,
    updated_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: ListEnabledRepoMapConfigs :many
SELECT * FROM repo_map_config WHERE enabled = 1 ORDER BY repo_id;

-- name: GetRepoMapGlobal :one
SELECT * FROM repo_map_global WHERE id = 1;

-- name: UpdateRepoMapGlobal :one
UPDATE repo_map_global
SET inject_labels = ?1, excluded_agent_ids = ?2, updated_at = CURRENT_TIMESTAMP
WHERE id = 1
RETURNING *;

-- name: CreateRepoMapRun :one
INSERT INTO repo_map_runs (id, repo_id, trigger, actor, status, provider_config_id, started_at)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)
RETURNING *;

-- name: FinishRepoMapRun :one
UPDATE repo_map_runs
SET status = ?1, commit_sha = ?2, page_count = ?3, char_count = ?4, error = ?5,
    cost_usd = ?6, input_tokens = ?7, output_tokens = ?8, finished_at = ?9
WHERE id = ?10
RETURNING *;

-- name: FailRunningRepoMapRuns :exec
UPDATE repo_map_runs
SET status = 'failed', error = 'interrupted by restart', finished_at = ?1
WHERE status = 'running';

-- name: ListRepoMapRuns :many
SELECT * FROM repo_map_runs WHERE repo_id = ?1
ORDER BY started_at DESC, id DESC
LIMIT ?2;

-- name: GetLatestRepoMapRun :one
SELECT * FROM repo_map_runs WHERE repo_id = ?1
ORDER BY started_at DESC, id DESC
LIMIT 1;

-- name: GetLatestSuccessfulRepoMapRun :one
SELECT * FROM repo_map_runs WHERE repo_id = ?1 AND status = 'succeeded'
ORDER BY started_at DESC, id DESC
LIMIT 1;

-- name: CreateRepoMapPage :one
INSERT INTO repo_map_pages (id, repo_id, run_id, slug, title, body, commit_sha, position)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)
RETURNING *;

-- name: ListRepoMapPagesForRun :many
SELECT * FROM repo_map_pages WHERE run_id = ?1 ORDER BY position ASC, id ASC;

-- name: PruneRepoMapPages :exec
DELETE FROM repo_map_pages
WHERE repo_map_pages.repo_id = ?1
  AND repo_map_pages.run_id NOT IN (
    SELECT r.id FROM repo_map_runs r
    WHERE r.repo_id = ?1 AND r.status = 'succeeded'
    ORDER BY r.started_at DESC, r.id DESC
    LIMIT ?2
  );
