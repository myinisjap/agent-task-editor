# internal/storage

SQLite database layer: connection management, schema migrations, sqlc-generated queries, and seed data.

## Files

| File | Purpose |
|---|---|
| `db.go` | Opens SQLite connection with WAL mode; runs migrations |
| `premigrate.go` | Pre-migration `VACUUM INTO` snapshot next to the DB file when migrations are pending (keeps 3; aborts startup on failure unless `SKIP_PREMIGRATE_SNAPSHOT=true`; skipped for `:memory:`/`file:` DSNs) |
| `seed.go` | Seeds the default workflow on first startup |
| `migrations/` | SQL migration files (`NNN_name.up.sql` / `NNN_name.down.sql`) |
| `queries/` | Source SQL files for sqlc codegen |
| `gen/` | **Auto-generated** Go code — do not hand-edit |

## Database

SQLite with WAL (Write-Ahead Logging) enabled for better concurrent read performance. The file path defaults to `agent-task-editor.db` (configurable via `DB_PATH`). `DB.Path()` returns the raw path passed to `Open`; `DB.Backup(ctx, dstPath)` writes a consistent snapshot via `VACUUM INTO` (safe under concurrent writes; `dstPath` must not exist), used by `GET /api/v1/backup` and `internal/backup` (`docs/backup.md`).

## Migrations

`golang-migrate`, files `NNN_description.up.sql` / `.down.sql` (zero-padded). Run at startup; when any are pending a `VACUUM INTO` snapshot is taken first. SQLite can't add a `NOT NULL REFERENCES` column to a populated table, so such changes use the table-swap pattern (see `008_agent_runs_fk_set_null`, `039_provider_configs`). Column defaults apply only to INSERTs that omit the column. Migration history is `git log` / the files themselves; design rationale is in `docs/adr/`.

## sqlc Code Generation

`sqlc.yaml` configures codegen. SQL query files are in `queries/`:

- `tasks.sql` — task CRUD + label moves + dispatch queries
- `human_decisions.sql` — one row per waiting_human run; question written at escalation, reply on DispatchReply; read by startRun for the HUMAN DECISIONS prompt section; not pruned by log retention
- `workflows.sql` — workflow, label, transition, history CRUD
- `agents.sql` — agent config CRUD
- `providers.sql` — provider config CRUD (provider/model/env, shared by agent configs and chat sessions)
- `chat.sql` — chat session CRUD (interactive PTY terminal sessions)
- `repos.sql` — repository CRUD
- `runs.sql` — agent run CRUD and log storage
- `previews.sql` — task preview screenshot captures + images
- `dashboard.sql` — aggregate queries for the dashboard
- `repo_lessons.sql` — curated per-repo/global lessons injected into agent prompts
- `review_comments.sql` — inline diff review comment CRUD, both locally-created and GitHub-ingested
- `pr_review_state.sql` — per-task GitHub PR review/check ingestion cursor (`task_pr_review_state`)

Long or paginated queries use positional `?N` params, not `@named` (ADR 0020). After editing any `.sql` file, run `sqlc generate` (or `go generate ./...` from the backend root) to regenerate `gen/`.

## Important Query Invariants

**Dispatch lock (ADR 0001–0003)**
- `UpdateTaskLabel` always sets `active_agent_run_id = NULL`. The engine's transition write is an equivalent raw-SQL **compare-and-swap** (`AND label = ?`) on the tx, because sqlc miscompiles the extra param (ADR 0020); the generated query is for tests/other callers.
- `SetTaskActiveRun` sets `current_` and `active_` run ids atomically. `SetTaskActiveRunOnly` sets only `active_agent_run_id`; use it for synthetic escalation runs (cost-budget phantom runs) that must not become the WS replay run or the source of rework feedback. The nil-provider escalation uses `SetTaskActiveRun` with `CurrentAgentRunID` restored.
- `ClearActiveAgentRun` (task id only) is reserved for `Rerun`'s human force-clear; every cleanup on behalf of a run uses `ClearActiveAgentRunIfOwner` (requires the caller's run id, `:execrows`).
- `ClaimTaskActiveRunIfFree` / `ClaimTaskActiveRunIfHeldBy` are the dispatch CAS claims.

**Pickup (ADR 0027)**
- `ListAgentPickupTasks` filters: `active_agent_run_id IS NULL`, `paused = 0`, not archived, `next_retry_at` not in the future, no unsatisfied blocker (`NOT EXISTS`, derived, never a column); orders `priority DESC, created_at ASC`. It is the only query whose `ORDER BY` affects dispatch order.
- Dependency edges (`task_dependencies`, FKs `ON DELETE CASCADE`) are added in a transaction with cycle detection before `CreateTaskDependency`. `ListTaskDependencyCountsForTasks` / `ListSubtaskRollupsForParents` are the `sqlc.slice` variants scoped to returned ids; use them on hot read paths, not the unscoped originals.
- Subtasks: `tasks.parent_task_id` (`ON DELETE SET NULL`), `created_by_run_id`, `merge_status` (``/`pending`/`merged`/`merge_conflict`); `create_subtask` inserts the child and the dependency edge in one transaction.
- `SearchTasks`: every param treats `''` as "no filter"; sqlc types them `interface{}`, pass strings; `archived` is tri-state.

**Ingestion and sync (ADR 0036)**
- `ListGhSyncEligibleTasks` filters in SQL to keep `gh` calls bounded.
- Tasks dedupe by `(source, source_ref)` partial unique index; schedule firings use `source_ref = "<schedule_id>#<marker>"` and `HasOpenTaskForSchedule` (`LIKE '<id>#%'`).
- `SetTaskPR` (now an upsert into `task_git`) sets `git_state` + `pr_url` together; callers preserve an existing URL when the live query surfaces none. `SetTaskPRMergeable` leaves `updated_at` alone.
- `repos.clone_status` (`ready`/`cloning`/`error`) + `clone_error` are written by `SetRepoCloneStatus` (async auto-clone).
- Repo-create/update handlers name every column, so defaults don't apply; see ADR 0036.

**Pagination (ADR 0025)** — `SearchTasksPage`, `ListAgentLogsPage`, `ListAgentRunsPage`, `ListProviderConfigsPage`, `ListAllAgentConfigsPage`, `ListReposPage`, `ListWorkflowsPage`: newest first, cursor = last row's id, positional `?N` params, ASCII-only comments. `ListAgentConfigs` (enabled only) and `ListIssueSyncRepos` stay unpaginated. `SumTaskCost` backs `Task.cumulative_cost_usd`.

**Other**
- `provider_configs` is referenced by `agent_configs` and `chat_sessions` via `provider_config_id`; delete is blocked (409) while referenced. `ListInUseProviders` backs `GET /health/providers` (ADR 0018).
- `backup_settings` is a singleton (`CHECK (id = 1)`) edited via `PUT /api/v1/backup/settings` (`interval_seconds >= 600`, `keep >= 1`) — ADR 0031.
- `agent_runs.session_id` + `GetLatestTaskSession` back `claude --resume` (ADR 0028).

> **Keep `queries/*.sql` strictly ASCII.** One multi-byte character (an em dash in a comment is enough) silently corrupts the generated SQL for the queries that follow it in that file; it surfaces only at runtime as `near "…": syntax error` (ADR 0020). Prefer a new per-table file over appending to `tasks.sql`. Check:
>
> ```bash
> grep -lP '[^\x00-\x7F]' internal/storage/queries/*.sql   # must print nothing
> ```
>
> Keep migrations ASCII too (they have proven tolerant, but it costs nothing).
>
> **Task columns (ADR 0037).** `tasks` holds board state only. Git/worktree/PR fields (`branch`, `worktree_path`, `base_ref`, `git_state`, `pr_url`, `pr_mergeable`) live in `task_git` and the write-back flags in `task_writeback` (lazy rows, upserted on write; missing row = old defaults). Reads select `*` from the `tasks_full` view (LEFT JOINs + COALESCE), which sqlc `rename` maps to `gen.Task`; the bare table is `gen.TaskBase`. When adding a `tasks` column, add it to the table **and** to a new migration that drops and recreates `tasks_full` (any migration that rebuilds `tasks` must do the same). `RETURNING` cannot read the view, so task writes that return a row are unexported `:exec` queries wrapped by `gen/tasks_compat.go` (hand-written; write, then `GetTask`). Do not hand-edit the generated files, but do keep `tasks_compat.go` in sync with `queries/tasks.sql`.

## Schema Key Tables

```
tasks               — title, description, type, label, repo_id, workflow_id,
                      current_agent_run_id, active_agent_run_id (board state)
task_git            — task_id, branch, worktree_path, base_ref, git_state, pr_url, pr_mergeable
task_writeback      — task_id, writeback_in_progress_sent/_pr_commented/_closed
tasks_full (view)   — tasks + task_git + task_writeback, the flat 37-column read model
workflow_labels     — name, color, sort_order, agent_ignore, is_terminal, verify_mode/verify_command (per-label verify override)
workflow_transitions — from_label, to_label, trigger_type (agent|human|both)
agent_runs          — task_id, status, feedback, started_at, completed_at
run_verifications   — run_id (PK), task_id, command, status pass|fail|timeout|error, exit_code, output tail (ADR 0038)
agent_logs          — agent_run_id, type, content, timestamp
task_label_history  — task_id, from_label, to_label, trigger, actor_id, note
```
