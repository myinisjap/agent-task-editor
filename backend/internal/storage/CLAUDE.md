# internal/storage

SQLite database layer: connection management, schema migrations, sqlc-generated queries, and seed data.

## Files

| File | Purpose |
|---|---|
| `db.go` | Opens SQLite connection with WAL mode; runs migrations |
| `seed.go` | Seeds the default workflow on first startup |
| `migrations/` | SQL migration files (`NNN_name.up.sql` / `NNN_name.down.sql`) |
| `queries/` | Source SQL files for sqlc codegen |
| `gen/` | **Auto-generated** Go code — do not hand-edit |

## Database

SQLite with WAL (Write-Ahead Logging) enabled for better concurrent read performance. The file path defaults to `agent-task-editor.db` (configurable via `DB_PATH`).

## Migrations

Managed by `golang-migrate`. Files must follow the `NNN_description.up.sql` / `NNN_description.down.sql` naming pattern where `NNN` is a zero-padded integer.

Current migrations:
- `001_initial_schema` — full initial schema
- `002_label_flags` — no-op (column defined in 001; later dropped in 009)
- `003_task_active_run` — adds `active_agent_run_id` to `tasks`
- `009_drop_rejection_target` — drops `is_rejection_target`; Approve/Reject now route via transition `path`

## sqlc Code Generation

`sqlc.yaml` configures codegen. SQL query files are in `queries/`:

- `tasks.sql` — task CRUD + label moves + dispatch queries
- `workflows.sql` — workflow, label, transition, history CRUD
- `agents.sql` — agent config CRUD
- `repos.sql` — repository CRUD
- `runs.sql` — agent run CRUD and log storage
- `dashboard.sql` — aggregate queries for the dashboard

After editing any `.sql` file, run `sqlc generate` (or `go generate ./...` from the backend root) to regenerate `gen/`.

## Important Query Invariants

- `UpdateTaskLabel` always sets `active_agent_run_id = NULL` — every label transition implicitly clears the dispatch lock. Note the workflow engine does **not** call this generated query for the transition write: it runs an equivalent raw-SQL **compare-and-swap** (adds `AND label = ?` on the expected from-label) directly on the tx, because sqlc's SQLite analyzer miscompiles that extra guard param (see the byte-offset note below). The generated `UpdateTaskLabel` is still used by tests/other callers.
- `ListGhSyncEligibleTasks` is the ghsync sweep's task source: `WHERE branch != '' AND archived = 0 AND git_state NOT IN ('pr_merged','pr_closed')`. Filtering in SQL (rather than listing all tasks and filtering in Go) keeps the number of `gh` calls per sweep bounded by open, branch-bearing work.
- `026_repo_clone_status` adds `repos.clone_status TEXT NOT NULL DEFAULT 'ready'` (`ready`/`cloning`/`error`) and `repos.clone_error TEXT NOT NULL DEFAULT ''`, written by `SetRepoCloneStatus`. Local repos and finished clones are `ready`; an auto-clone (`POST /repos` with only a `remote_url`) inserts the row, flips it to `cloning`, runs `git clone` in a background goroutine, then sets `ready` or `error` and publishes a `repo.clone_done` / `repo.clone_failed` WS event.
- `ListAgentPickupTasks` filters on `active_agent_run_id IS NULL` — only unlocked tasks are dispatched.
- `ListAgentPickupTasks` also filters on `paused = 0` — a manually paused task is never dispatched, regardless of label.
- `029_subtasks` adds `tasks.parent_task_id` (FK `ON DELETE SET NULL` — deleting a parent orphans children to top-level), `tasks.created_by_run_id` (provenance), and `tasks.merge_status` (``/`pending`/`merged`/`merge_conflict`), plus `agent_configs.subtasks_enabled`/`max_subtasks`. Subtask queries (`CreateSubtask`, `ListSubtasks`, `CountSubtasks`, `SetTaskMergeStatus`, `ListSubtaskRollups`) live in `queries/tasks.sql`. `ListSubtaskRollups` returns per-parent `total`/`done`/`conflicts` (SUM columns come back as `*float64`) for the board rollup. The `create_subtask` endpoint (`POST /tasks/{id}/subtasks`) inserts the child + the parent→child dependency edge in one transaction; the `SubtaskCoordinator` (agent package) merges a child's branch back into the parent's on terminal and auto-advances the parent. **When adding a `tasks` column, append it to every explicit column list in `tasks.sql`** (including the `t.`-prefixed `ListAgentPickupTasks`) or sqlc emits a per-query row struct instead of mapping to `gen.Task`.
- `028_task_dependencies` adds the `task_dependencies (task_id, depends_on_task_id)` table — peer dependency edges (both FKs `ON DELETE CASCADE`). `ListAgentPickupTasks` grows a `NOT EXISTS (unsatisfied blocker)` clause: a task with any blocker that is neither archived nor on a terminal label is never dispatched. "Blocked" is derived (never a column), so no event/write is needed when a blocker finishes — the next sweep just sees the task differently. Queries live in `queries/dependencies.sql`; edge writes (add) go through a transaction that does cycle detection (`ListWorkflowDependencyEdges` + Go DFS) before `CreateTaskDependency`. `ListTaskDependencyCounts` returns derived `blocked_by_count`/`blocking_count` for every task that participates in an edge, so `GET /tasks` renders badges without N+1.
- `SetTaskActiveRun` sets both `current_agent_run_id` and `active_agent_run_id` atomically — used by dispatcher when creating a new run.
- `ClearActiveAgentRun` sets only `active_agent_run_id = NULL` — used by pool on run completion.
- `015_task_paused` adds `tasks.paused INTEGER NOT NULL DEFAULT 0`, toggled via `SetTaskPaused` / `PATCH /tasks/{id}/pause`. It's independent of `label` and `active_agent_run_id`, and persists across restarts (it's a column, not in-memory state).
- `022_task_source` adds `tasks.source`/`tasks.source_ref` plus a partial unique index on `(source, source_ref) WHERE source != ''` — the GitHub Issues importer (`internal/tasksource`) checks `CountTasksBySource` before `CreateSourcedTask`, and the index guards against concurrent-insert races. Manually created tasks keep both fields `''`.
- `023_task_archived` adds `tasks.archived INTEGER NOT NULL DEFAULT 0`, toggled via `SetTaskArchived` / `PATCH /tasks/{id}/archive`. Archived tasks are excluded from `ListAgentPickupTasks` (never dispatched), skipped by the ghsync sweep, and hidden from `GET /tasks` unless `archived=all|only` is passed. Like `paused`, it's independent of `label`.
- `SearchTasks` is the filterable listing behind `GET /tasks` — every param (`@query` free-text over title/description, `@label`, `@repo_id`, `@type`, `@git_state`, tri-state `@archived`) treats `''` as "no filter". sqlc types these params `interface{}` (it can't infer types from the `@x = '' OR col = @x` pattern); pass strings.
- `024_task_templates` adds the `task_templates` table (unique `name`; pre-filled `title`/`description`/`type` for the new-task form). CRUD queries live in `queries/templates.sql`.
- `025_task_pr_url` adds `tasks.pr_url TEXT NOT NULL DEFAULT ''` — the GitHub PR URL for the task's branch. Written by `SetTaskPR` (which sets `git_state` + `pr_url` together) from `POST /tasks/{id}/pr`, the `GET /tasks/{id}/github-status` refresh, and the ghsync sweep. Those callers preserve any existing URL when the live query surfaces none, so a valid link is never blanked out.
- `027_session_resume` adds `agent_runs.session_id TEXT NOT NULL DEFAULT ''` (the provider CLI conversation session, written by the pool via `SetAgentRunSession`; `GetLatestTaskSession` fetches the newest non-empty one per (task, agent config) for `claude --resume`) and `agent_configs.resume_sessions INTEGER NOT NULL DEFAULT 1` (per-config opt-out).
- **Cursor pagination:** `SearchTasksPage` (behind `GET /tasks`) and `ListAgentLogsPage` (behind `GET /tasks/{id}/runs/{run_id}/logs` and WS replay) are cursor-paginated, both **newest first**. Tasks cursor on `(created_at, id)`, logs on `(timestamp, id)` — id is a stable tiebreaker so the cursor is a total order even when the timestamp ties. The cursor is the last row's **id**; the query resolves that id's own `created_at`/`timestamp` (via a self-referential comparison) so the comparison stays consistent with the `ORDER BY` regardless of how the timestamp text is formatted. **Both queries use positional `?N` params, not `@named` ones** — sqlc's SQLite analyzer has a byte-offset bug that corrupts long named-parameter queries (it silently eats characters and emits `?N` garbage), so keep them positional; the arg order is documented in each query's comment. `SELECT t.*`/`SELECT l.*` (rather than an explicit column list) also keeps the query text short enough to dodge the same bug while still mapping to the full `Task`/`AgentLog` struct.

## Schema Key Tables

```
tasks               — title, description, type, label, repo_id, workflow_id,
                      current_agent_run_id, active_agent_run_id
workflow_labels     — name, color, sort_order, agent_ignore, is_terminal
workflow_transitions — from_label, to_label, trigger_type (agent|human|both)
agent_runs          — task_id, status, feedback, started_at, completed_at
agent_logs          — agent_run_id, type, content, timestamp
task_label_history  — task_id, from_label, to_label, trigger, actor_id, note
```
