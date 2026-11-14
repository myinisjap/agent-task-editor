-- name: UpsertTaskPRSource :exec
INSERT INTO task_pr_source (task_id, repo_id, pr_number, pr_url, pr_title, pr_body, pr_author, head_ref, head_sha, base_ref, pr_state)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id) DO UPDATE SET
    pr_url = excluded.pr_url,
    pr_title = excluded.pr_title,
    pr_body = excluded.pr_body,
    pr_author = excluded.pr_author,
    head_ref = excluded.head_ref,
    head_sha = excluded.head_sha,
    base_ref = excluded.base_ref,
    pr_state = excluded.pr_state,
    updated_at = CURRENT_TIMESTAMP;

-- name: GetTaskPRSource :one
SELECT * FROM task_pr_source WHERE task_id = ?;

-- name: ListTaskPRSourcesForTasks :many
SELECT * FROM task_pr_source WHERE task_id IN (sqlc.slice('task_ids'));

-- name: SetTaskPRSourceReviewed :exec
UPDATE task_pr_source
SET reviewed_sha = ?, last_posted_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE task_id = ?;

-- name: CreatePRReviewPost :exec
INSERT INTO task_pr_review_posts (id, task_id, agent_run_id, head_sha, summary, comments_json, resolved_json, seq)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(agent_run_id) DO NOTHING;

-- name: ListPendingPRReviewPosts :many
SELECT * FROM task_pr_review_posts WHERE task_id = ? AND status = 'pending' ORDER BY seq;

-- name: ListPostedPRReviewPosts :many
SELECT * FROM task_pr_review_posts WHERE task_id = ? AND status = 'posted' ORDER BY seq;

-- name: MarkPRReviewPostPosted :exec
UPDATE task_pr_review_posts
SET status = 'posted', posted_mode = ?, forge_ref = ?, posted_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: NextPRReviewPostSeq :one
SELECT CAST(COALESCE(MAX(seq), 0) + 1 AS INTEGER) FROM task_pr_review_posts WHERE task_id = ?;

-- name: ListPRReviewRepos :many
SELECT * FROM repos WHERE pr_review_enabled != 0 AND remote_url IS NOT NULL AND remote_url != '' ORDER BY created_at DESC;

-- name: SetRepoPRReview :one
UPDATE repos
SET pr_review_enabled = ?, pr_review_author_assocs = ?, pr_review_label = ?, pr_review_skip_drafts = ?, pr_review_workflow_id = ?, pr_review_target_label = ?
WHERE id = ?
RETURNING *;

-- name: CountNonPRReviewWorkflows :one
SELECT COUNT(*) FROM workflows WHERE name != 'PR review';

-- name: HasSeedMarker :one
SELECT COUNT(*) FROM seed_markers WHERE name = ?;

-- name: AddSeedMarker :exec
INSERT OR IGNORE INTO seed_markers (name) VALUES (?);
