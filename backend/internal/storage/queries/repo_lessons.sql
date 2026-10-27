-- name: CreateRepoLesson :one
INSERT INTO repo_lessons (id, repo_id, kind, body, path_globs, stages, citations, source, source_task_id, status)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10)
RETURNING *;

-- name: GetRepoLesson :one
SELECT * FROM repo_lessons WHERE id = ?1;

-- name: ListRepoLessons :many
SELECT * FROM repo_lessons
WHERE repo_id = ?1 AND (?2 = '' OR status = ?2)
ORDER BY created_at DESC, id DESC;

-- name: ListGlobalLessons :many
SELECT * FROM repo_lessons
WHERE repo_id IS NULL AND (?1 = '' OR status = ?1)
ORDER BY created_at DESC, id DESC;

-- name: ListActiveLessonsForRepo :many
SELECT * FROM repo_lessons
WHERE status = 'active' AND (repo_id = ?1 OR repo_id IS NULL)
ORDER BY created_at DESC, id DESC;

-- name: UpdateRepoLesson :one
UPDATE repo_lessons
SET kind = ?2, body = ?3, path_globs = ?4, stages = ?5, citations = ?6,
    status = ?7, source = ?8, source_task_id = ?9, updated_at = CURRENT_TIMESTAMP
WHERE id = ?1
RETURNING *;

-- name: DeleteRepoLesson :execrows
DELETE FROM repo_lessons WHERE id = ?1;

-- name: IncrementLessonInjectedCount :exec
UPDATE repo_lessons SET injected_count = injected_count + 1 WHERE id IN (sqlc.slice('ids'));
