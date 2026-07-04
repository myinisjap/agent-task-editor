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

- `UpdateTaskLabel` always sets `active_agent_run_id = NULL` — every label transition implicitly clears the dispatch lock.
- `ListAgentPickupTasks` filters on `active_agent_run_id IS NULL` — only unlocked tasks are dispatched.
- `ListAgentPickupTasks` also filters on `paused = 0` — a manually paused task is never dispatched, regardless of label.
- `SetTaskActiveRun` sets both `current_agent_run_id` and `active_agent_run_id` atomically — used by dispatcher when creating a new run.
- `ClearActiveAgentRun` sets only `active_agent_run_id = NULL` — used by pool on run completion.
- `015_task_paused` adds `tasks.paused INTEGER NOT NULL DEFAULT 0`, toggled via `SetTaskPaused` / `PATCH /tasks/{id}/pause`. It's independent of `label` and `active_agent_run_id`, and persists across restarts (it's a column, not in-memory state).
- `022_task_source` adds `tasks.source`/`tasks.source_ref` plus a partial unique index on `(source, source_ref) WHERE source != ''` — the GitHub Issues importer (`internal/tasksource`) checks `CountTasksBySource` before `CreateSourcedTask`, and the index guards against concurrent-insert races. Manually created tasks keep both fields `''`.

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
