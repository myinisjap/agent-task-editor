# 37. Git/PR state and write-back flags live in side tables, read through a view

Status: Accepted

## Context

`tasks` had grown to 37 columns mixing board state, git/worktree/branch/PR
state, mergeability, write-back flags, retry state and cost. sqlc forces every
query that returns a task to name every column (otherwise it emits a per-query
row struct instead of `gen.Task`), so each new column meant editing two dozen
column lists.

## Decision

- Migration 063 moves `branch`, `worktree_path`, `base_ref`, `git_state`,
  `pr_url`, `pr_mergeable` into `task_git`, and `writeback_in_progress_sent`,
  `writeback_pr_commented`, `writeback_closed` into `task_writeback`. Both are
  keyed by `task_id` with `ON DELETE CASCADE`; column names are unchanged.
- Side rows are created lazily: the writers are upserts
  (`INSERT ... SELECT id, ... FROM tasks WHERE id = ? ON CONFLICT DO UPDATE`),
  so an unknown task id stays a silent no-op and a task with no row simply has
  the old defaults (`''` / `0`). The migration copies only tasks with
  non-default values.
- Reads go through the `tasks_full` view: `tasks` LEFT JOIN both side tables,
  `COALESCE`d to the old defaults, with all 37 columns in their historical
  order. sqlc `rename` maps the view to `gen.Task` (and the bare table to
  `gen.TaskBase`), so the API JSON and every `gen.Task` consumer are
  unchanged and read queries are `SELECT * FROM tasks_full`.
- `RETURNING` cannot read the view, so the task write queries that used to
  return the row are `:exec` queries under unexported names and
  `gen/tasks_compat.go` (hand-written, not generated) keeps the exported
  signatures: write, then `GetTask`. The git writers pair an upsert with a
  separate `updated_at` bump, except `SetTaskPRMergeable`, which still leaves
  `updated_at` alone. The two statements are not one transaction; losing the
  bump on a crash between them is harmless.

## Consequences

- A new task-board column is added to `tasks` and the `tasks_full` view (a new
  migration that drops and recreates the view); read queries do not change.
  A new git or write-back field goes in the side table and the view.
- Any migration that rebuilds `tasks` must drop and recreate `tasks_full`.
- Do not add `RETURNING` of the full row to a new task write; add an unexported
  `:exec` query and a wrapper in `tasks_compat.go`.
- The down migration restores the columns and copies the data back.
