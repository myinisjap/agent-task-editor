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

`DB.Path()` returns the raw path passed to `Open` (no `?_foreign_keys=on&...` DSN suffix) — used by the backup handler/scheduler to locate a writable directory on the same filesystem as the live DB. `DB.Backup(ctx, dstPath)` writes a consistent point-in-time snapshot via `VACUUM INTO` (safe under concurrent write load, unlike a raw file copy of a WAL-mode database); `dstPath` must not already exist. Used by both `GET /api/v1/backup` (`internal/api/handlers/backup.go`) and the optional automatic-backup scheduler (`internal/backup`) — see `docs/backup.md`.

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
- `providers.sql` — provider config CRUD (provider/model/env, shared by agent configs and chat sessions)
- `chat.sql` — chat session CRUD (interactive PTY terminal sessions)
- `repos.sql` — repository CRUD
- `runs.sql` — agent run CRUD and log storage
- `dashboard.sql` — aggregate queries for the dashboard
- `review_comments.sql` — inline diff review comment CRUD, both locally-created and GitHub-ingested
- `pr_review_state.sql` — per-task GitHub PR review/check ingestion cursor (`task_pr_review_state`)

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
- `031_task_priority` adds `tasks.priority INTEGER NOT NULL DEFAULT 0` (-1 low / 0 normal / 1 high / 2 urgent — constants in `internal/api/handlers/tasks.go`). `ListAgentPickupTasks` orders by `priority DESC, created_at ASC` — this is the only query with an `ORDER BY` that affects dispatch order; priority only changes ordering, it never preempts an in-flight run or bypasses any other dispatch gate (paused/archived/blocked/retry-backoff/cost-budget). `GET /tasks` and `GET /tasks/{id}` also surface a derived `queue_position` (0-based rank in a fresh `ListAgentPickupTasks` call, via `queuePositionMap`/`applyQueuePosition`) for tasks currently pickup-eligible.
- `035_task_schedules` adds the `task_schedules` table (`template_id`/`repo_id` FKs `ON DELETE CASCADE`, `cron_expr`, `target_label` default `not_ready`, `enabled`, `last_run_at`). Queries live in `queries/schedules.sql`; fired by `internal/schedule.Scheduler` (poll-loop shaped like `tasksource.Importer`). Each firing creates a task via the existing `CreateSourcedTask` with `source = "schedule"` and `source_ref = "<schedule_id>#<run marker>"` — **not** a bare schedule id, because `source_ref` is unique per `(source, source_ref)` (the `022_task_source` partial index) but a schedule fires repeatedly, so each firing needs its own ref. `HasOpenTaskForSchedule` (uses `sqlc.arg(...)` + `LIKE '<schedule_id>#%'` to dedupe by schedule despite the varying suffix) mirrors the dependency-satisfaction idiom from `028_task_dependencies`: "open" = not archived AND not on a workflow label with `is_terminal != 0`. The sweep skips firing while an open task from a prior firing still exists, so re-running a schedule never stacks a new task on an unfinished one. Cron parsing/evaluation is `internal/cronexpr` (dependency-free, standard 5-field cron subset: `*`, comma lists, `*/N` steps — no ranges or named days/months).
- `036_backup_settings` adds the `backup_settings` singleton table (`id` fixed at `1` via `CHECK (id = 1)`, `interval_seconds`, `keep`, `updated_at`), seeded with the same defaults as `config.Defaults()`'s `BACKUP_INTERVAL`/`BACKUP_KEEP` (86400s / 7) so the DB-backed settings path never surfaces an empty row. Queries (`GetBackupSettings`, `UpsertBackupSettings`) live in `queries/backup_settings.sql`; `UpsertBackupSettings` uses `ON CONFLICT (id) DO UPDATE` against the single row. Read/written via `GET`/`PUT /api/v1/backup/settings` (`internal/api/handlers/backup_settings.go`), which enforces `interval_seconds >= 600` (10 min floor) and `keep >= 1`. `internal/backup.Scheduler` (when constructed via `NewWithSettingsFunc`, as `cmd/server/main.go` does whenever `BACKUP_DIR` is set) re-reads this row before every scheduled run via a `time.Timer` re-armed each iteration (not a `time.Ticker`, which is fixed at construction), so a settings change takes effect on the next run without a restart — `backup.MinInterval` (10 min) is enforced there too as a last line of defense regardless of what a settings func returns.
- `039_provider_configs` splits the provider/model/API-key (env vars) triple out of `agent_configs` into its own reusable `provider_configs` table (`name`, `provider`, `model`, `env` — a JSON object of env var overrides, same shape `agent_configs.env` used), so the same provider setup (e.g. an Anthropic API key) can be referenced by both an `agent_configs` row (workflow dispatch) and a `chat_sessions` row (interactive PTY terminal), rather than each owning its own copy. `agent_configs` and `chat_sessions` each gain `provider_config_id TEXT NOT NULL REFERENCES provider_configs(id)` and lose their own inline provider/model(/env) columns (`chat_sessions` never had an `env` column — see `037_chat_sessions` — so its backfilled provider config defaults `env` to `{}`). Because SQLite can't add a `NOT NULL REFERENCES` column to a non-empty table, the up-migration backfills one `provider_configs` row per pre-existing `agent_configs`/`chat_sessions` row **reusing that row's own id as the new `provider_configs.id`** (both are already globally unique, sidestepping any positional-zip ambiguity), then rebuilds both tables (same table-swap pattern as `008_agent_runs_fk_set_null`) with `provider_config_id` wired to `id`. Queries live in `queries/providers.sql`; CRUD is exposed via `internal/api/handlers/providers.go` (`GET/POST /api/v1/provider-configs`, `GET/PUT/DELETE /api/v1/provider-configs/{id}`), and delete is blocked (409) while any `agent_configs`/`chat_sessions` row still references the config. `internal/agent.AgentConfig` (the in-package struct read by every provider implementation) keeps flat `Provider`/`Model`/`Env` fields — `dispatcher.go#toAgentConfig` now populates them from the joined `ProviderConfig`, and `handlers/chat.go` resolves the session's `ProviderConfig` to the provider/model strings passed to `TerminalManager.Attach`, so the individual provider files needed no changes. `ListInUseProviders` (also in `providers.sql`) returns the distinct providers referenced by an *enabled* agent config or *any* chat session, backing `GET /api/v1/health/providers`.
- `040_pr_review_feedback` adds `repos.pr_review_auto_transition_enabled INTEGER NOT NULL DEFAULT 0` and, to `task_review_comments`, `external_id TEXT` (nullable) + `source TEXT NOT NULL DEFAULT 'local'` (`'local'`|`'github'`), plus a partial unique index `(task_id, external_id) WHERE external_id IS NOT NULL` so re-ingesting the same GitHub comment is a no-op via `GetTaskReviewCommentByExternalID` before `CreateGitHubTaskReviewComment`. Also creates `task_pr_review_state` (`task_id` PK `ON DELETE CASCADE`, `head_sha`, `last_review_submitted_at`, `last_comment_id`, `last_failed_check_sha`) — the ghsync sweep's per-task ingestion cursor for GitHub PR reviews/checks (`GetTaskPRReviewState`/`UpsertTaskPRReviewState` in `queries/pr_review_state.sql`). See `internal/ghsync/pr_review.go` and `docs/task-sources.md`'s "PR review / GitHub Actions feedback ingestion" section for the full behavior.

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
