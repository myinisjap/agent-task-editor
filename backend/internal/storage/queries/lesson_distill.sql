-- Repo lesson distiller: run records, dismissed-proposal memory and the
-- evidence reads. Evidence is untrusted text; callers fence it before it
-- reaches a model. Proposals themselves are repo_lessons rows (status
-- 'proposed', source 'distiller').

-- name: CreateLessonDistillRun :one
INSERT INTO lesson_distill_runs (id, repo_id, trigger, actor, status, provider_config_id, started_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: FinishLessonDistillRun :one
UPDATE lesson_distill_runs
SET status = ?1, evidence_count = ?2, proposal_count = ?3, error = ?4,
    cost_usd = ?5, input_tokens = ?6, output_tokens = ?7, finished_at = ?8
WHERE id = ?9
RETURNING *;

-- name: ListLessonDistillRuns :many
SELECT * FROM lesson_distill_runs
WHERE repo_id = ?1
ORDER BY started_at DESC, rowid DESC
LIMIT ?2;

-- name: GetLatestLessonDistillRun :one
SELECT * FROM lesson_distill_runs
WHERE repo_id = ?1
ORDER BY started_at DESC, rowid DESC
LIMIT 1;

-- name: FailRunningLessonDistillRuns :exec
-- Startup recovery: a run left 'running' by a crash or restart never finishes.
UPDATE lesson_distill_runs
SET status = 'failed', error = 'interrupted by a server restart', finished_at = ?1
WHERE status = 'running';

-- name: DeleteOldLessonDistillRuns :execrows
DELETE FROM lesson_distill_runs WHERE status <> 'running' AND started_at < ?1;

-- name: CreateLessonDismissal :exec
INSERT INTO lesson_dismissals (id, repo_id, body, dismissed_by)
VALUES (?1, ?2, ?3, ?4);

-- name: ListLessonDismissals :many
SELECT * FROM lesson_dismissals WHERE repo_id = ?1 ORDER BY created_at DESC, rowid DESC;

-- name: ListLessonsForDedupe :many
-- Every lesson that applies to the repo, any status, plus global ones.
SELECT * FROM repo_lessons WHERE repo_id = ?1 OR repo_id IS NULL;

-- name: ListRepoRejectionNotes :many
-- Human exits down a failure path with a note, for tasks of one repo. Moves
-- made by the PR-feedback sync or the autonomy policy are not human decisions.
SELECT h.task_id AS task_id, t.title AS title, h.to_label AS to_label,
       CAST(h.note AS TEXT) AS note, h.created_at AS created_at
FROM task_label_history h
JOIN tasks t ON t.id = h.task_id
WHERE t.repo_id = sqlc.arg(repo_id)
  AND h.trigger = 'human'
  AND (h.actor_id IS NULL OR h.actor_id NOT IN ('pr-feedback-sync', 'policy'))
  AND h.note IS NOT NULL AND TRIM(h.note) != ''
  AND h.note NOT LIKE 'Forced move%'
  AND h.created_at >= CAST(sqlc.arg(since) AS TEXT)
  AND EXISTS (
      SELECT 1 FROM workflow_transitions wt
      WHERE wt.workflow_id = t.workflow_id AND wt.from_label = h.from_label
        AND wt.to_label = h.to_label AND wt.trigger_type IN ('human', 'both')
        AND wt.path = 'failure')
ORDER BY h.created_at DESC, h.rowid DESC
LIMIT sqlc.arg(max_rows);

-- name: ListRepoHumanReplies :many
SELECT d.task_id AS task_id, t.title AS title, CAST(COALESCE(d.question, '') AS TEXT) AS question,
       CAST(d.reply AS TEXT) AS reply, d.replied_at AS replied_at
FROM task_human_decisions d
JOIN tasks t ON t.id = d.task_id
WHERE t.repo_id = sqlc.arg(repo_id)
  AND d.reply IS NOT NULL AND TRIM(d.reply) != ''
  AND d.replied_at >= CAST(sqlc.arg(since) AS TEXT)
ORDER BY d.replied_at DESC, d.rowid DESC
LIMIT sqlc.arg(max_rows);

-- name: ListRepoRunFeedback :many
-- Failure feedback routed to a rework run (agent review, tester, verify).
SELECT r.task_id AS task_id, t.title AS title, CAST(r.feedback AS TEXT) AS feedback, r.created_at AS created_at
FROM agent_runs r
JOIN tasks t ON t.id = r.task_id
WHERE t.repo_id = sqlc.arg(repo_id)
  AND r.feedback IS NOT NULL AND TRIM(r.feedback) != ''
  AND r.created_at >= CAST(sqlc.arg(since) AS TEXT)
ORDER BY r.created_at DESC, r.rowid DESC
LIMIT sqlc.arg(max_rows);

-- name: ListRepoVerifyFailures :many
SELECT v.task_id AS task_id, t.title AS title, v.command AS command, v.status AS status,
       v.output AS output, v.created_at AS created_at
FROM run_verifications v
JOIN tasks t ON t.id = v.task_id
WHERE t.repo_id = sqlc.arg(repo_id)
  AND v.status <> 'pass'
  AND v.created_at >= CAST(sqlc.arg(since) AS TEXT)
ORDER BY v.created_at DESC, v.rowid DESC
LIMIT sqlc.arg(max_rows);

-- name: ListRepoShouldHaveAsked :many
SELECT e.task_id AS task_id, t.title AS title, CAST(COALESCE(e.note, '') AS TEXT) AS note, e.created_at AS created_at
FROM autonomy_escapes e
JOIN tasks t ON t.id = e.task_id
WHERE t.repo_id = sqlc.arg(repo_id)
  AND e.source = 'should_have_asked'
  AND e.created_at >= CAST(sqlc.arg(since) AS TEXT)
ORDER BY e.created_at DESC, e.rowid DESC
LIMIT sqlc.arg(max_rows);

-- name: CountRepoDistillSignals :one
-- Cheap check for the scheduler: any evidence row newer than a cutoff? Mirrors
-- the evidence reads above (rejections, replies, run feedback, verify
-- failures, should-have-asked notes).
SELECT
  (SELECT COUNT(*) FROM task_label_history h JOIN tasks t ON t.id = h.task_id
     WHERE t.repo_id = sqlc.arg(repo_id) AND h.trigger = 'human'
       AND (h.actor_id IS NULL OR h.actor_id NOT IN ('pr-feedback-sync', 'policy'))
       AND h.note IS NOT NULL AND TRIM(h.note) != ''
       AND h.created_at > CAST(sqlc.arg(after) AS TEXT))
  + (SELECT COUNT(*) FROM task_human_decisions d JOIN tasks t ON t.id = d.task_id
     WHERE t.repo_id = sqlc.arg(repo_id) AND d.reply IS NOT NULL
       AND d.replied_at > CAST(sqlc.arg(after) AS TEXT))
  + (SELECT COUNT(*) FROM agent_runs r JOIN tasks t ON t.id = r.task_id
     WHERE t.repo_id = sqlc.arg(repo_id) AND r.feedback IS NOT NULL AND TRIM(r.feedback) != ''
       AND r.created_at > CAST(sqlc.arg(after) AS TEXT))
  + (SELECT COUNT(*) FROM run_verifications v JOIN tasks t ON t.id = v.task_id
     WHERE t.repo_id = sqlc.arg(repo_id) AND v.status <> 'pass'
       AND v.created_at > CAST(sqlc.arg(after) AS TEXT))
  + (SELECT COUNT(*) FROM autonomy_escapes e JOIN tasks t ON t.id = e.task_id
     WHERE t.repo_id = sqlc.arg(repo_id) AND e.source = 'should_have_asked'
       AND e.created_at > CAST(sqlc.arg(after) AS TEXT))
  AS n;
