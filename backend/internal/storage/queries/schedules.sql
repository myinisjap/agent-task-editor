-- Recurring task schedules: fire a task_template on a cron schedule against a
-- repo. "Open task for schedule" (see HasOpenTaskForSchedule) mirrors the
-- dependency-satisfaction idiom in dependencies.sql: a task is no longer
-- open once it is archived or sits on a terminal workflow label.
--
-- Each firing creates a task with source = 'schedule' and
-- source_ref = '<schedule_id>#<run marker>' (see internal/schedule) so that
-- repeated firings of the same schedule don't collide with the
-- UNIQUE(source, source_ref) index on tasks.

-- name: ListTaskSchedules :many
SELECT id, template_id, repo_id, cron_expr, target_label, enabled, last_run_at, created_at, updated_at FROM task_schedules ORDER BY created_at DESC;

-- name: ListEnabledTaskSchedules :many
SELECT id, template_id, repo_id, cron_expr, target_label, enabled, last_run_at, created_at, updated_at FROM task_schedules WHERE enabled != 0 ORDER BY created_at ASC;

-- name: GetTaskSchedule :one
SELECT id, template_id, repo_id, cron_expr, target_label, enabled, last_run_at, created_at, updated_at FROM task_schedules WHERE id = ?;

-- name: CreateTaskSchedule :one
INSERT INTO task_schedules (id, template_id, repo_id, cron_expr, target_label, enabled)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id, template_id, repo_id, cron_expr, target_label, enabled, last_run_at, created_at, updated_at;

-- name: UpdateTaskSchedule :one
UPDATE task_schedules
SET cron_expr = ?, target_label = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING id, template_id, repo_id, cron_expr, target_label, enabled, last_run_at, created_at, updated_at;

-- name: DeleteTaskSchedule :exec
DELETE FROM task_schedules WHERE id = ?;

-- name: SetTaskScheduleLastRun :exec
UPDATE task_schedules SET last_run_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: HasOpenTaskForSchedule :one
-- An "open" task for a schedule is one that hasn't reached a terminal label
-- and hasn't been archived. Used to skip firing a schedule again while a
-- prior run from it is still in flight.
--
-- Because a schedule fires repeatedly, its tasks can't share one source_ref
-- (tasks has a UNIQUE(source, source_ref) index for source != ''). Instead
-- each firing gets a source_ref of "<schedule_id>#<run marker>", and this
-- query matches by the "<schedule_id>#" prefix.
SELECT COUNT(*) FROM tasks t
WHERE t.source = 'schedule' AND t.source_ref LIKE sqlc.arg(schedule_id_prefix) || '#%' AND t.archived = 0
  AND NOT EXISTS (
      SELECT 1 FROM workflow_labels wl
      WHERE wl.workflow_id = t.workflow_id
        AND wl.name = t.label
        AND wl.is_terminal != 0
  );
