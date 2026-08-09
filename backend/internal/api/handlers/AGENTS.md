# internal/api/handlers

One file per resource group. All handlers receive a `*gen.Queries` for database access plus resource-specific dependencies.

## Files

| File | Handler | Key Dependencies |
|---|---|---|
| `tasks.go` | `TasksHandler` (CRUD, list/search, notes, label history, transitions) | `gen.Queries`, `workflow.Engine` |
| `task_response.go` | helpers for `TasksHandler` (wire-format wrapper + derived dependency/subtask/queue-position fields) | — |
| `task_uploads.go` | helper for `TasksHandler` (multipart attachment save) | — |
| `task_bulk.go` | `TasksHandler` (pause/archive toggles + bulk action) | — |
| `task_runs.go` | `TasksHandler` (run list/get/logs/cancel/reply) | `agent` (error sentinels) |
| `task_pr.go` | `TasksHandler` (diff/pr/pr-url/github-status/git-state) | `ghclient`, `agent.PushBranch` |
| `workflows.go` | `WorkflowsHandler` | `gen.Queries`, `*sql.DB` (for YAML import transactions) |
| `agents.go` | `AgentsHandler` | `gen.Queries` |
| `providers.go` | `ProviderConfigsHandler` — CRUD for Provider Configs (provider/model/env), the entity `agent_configs`/`chat_sessions` reference by `provider_config_id` | `gen.Queries` |
| `chat.go` | `ChatHandler` — interactive chat session CRUD + PTY terminal WebSocket upgrade, separate from the task/workflow state machine | `gen.Queries`, `Terminal` (`agent.TerminalManager`) |
| `repos.go` | `ReposHandler` | `gen.Queries`, `repoBaseDir string` |
| `review_comments.go` | `ReviewCommentsHandler` | `gen.Queries` |
| `templates.go` | `TemplatesHandler` | `gen.Queries` |
| `dashboard.go` | `DashboardHandler` | `gen.Queries` |
| `health.go` | `Health` func | none |
| `health_providers.go` | `HealthHandler` | `gen.Queries`, mcp/repo/llm config, backup dir/interval/keep |
| `backup.go` | `BackupHandler` | `*storage.DB` |
| `backup_settings.go` | `BackupSettingsHandler` | `gen.Queries` |
| `workflow_yaml.go` | helpers for `WorkflowsHandler` | — |

## Tasks Handler Notes

- **Approve** (`POST /tasks/{id}/approve`) — follows the `success` human transition from the task's current label (via `humanPathTarget`), then calls `engine.Transition`. Returns `400` if no such transition exists.
- **Reject** (`POST /tasks/{id}/reject`) — follows the `failure` human transition from the current label; the optional `to_label` body field overrides it. Returns `400` if no `failure` transition exists and no override is given.
- **MoveLabel** (`PATCH /tasks/{id}/label`) — human-triggered move validated through `engine.Transition`; used by board drag-and-drop. `Approve`, `Reject`, and `MoveLabel` all call `rejectTransitionWhileRunLive` (backed by `taskRunIsLive`/`runLiveStatuses`) first and return `409` if the task's active agent run is `pending` or `running` — `engine.Transition`'s CAS unconditionally clears `active_agent_run_id`, so transitioning over a live run would silently release the dispatch lock and let the next sweep double-dispatch onto the same worktree (issue #244).
- **List** (`GET /tasks`) — backed by the cursor-paginated `SearchTasksPage` query; supports `q` (title/description substring), `label`, `repo_id`, `type`, `git_state`, and tri-state `archived` (`''` hides archived, `only`, `all`). Invalid `archived` values return `400`. Paginates newest-first with `?limit=` (default 200, cap 500) and `?after=<cursor>`; the body stays a plain array and the next-page cursor (the last task's id) is returned in the `X-Next-Cursor` header (absent on the final page). Fetches `limit+1` internally to decide whether to emit the header.
- **ListRuns** (`GET /tasks/{id}/runs`, in `task_runs.go`) — same cursor-pagination shape as `List` above, backed by `ListAgentRunsPage`. `?limit=` (default 100, cap 500) / `?after=<cursor>`, `X-Next-Cursor` on more pages. A task's *lifetime* cumulative cost is not derivable from a single page (a long-lived task with retries/reruns can have unbounded runs); it's surfaced separately as `Task.cumulative_cost_usd` (computed via `SumTaskCost` in `Get`, `tasks.go`) so the frontend cost badge doesn't need to sum a page of runs.
- **GetRunLogs** (`GET /tasks/{id}/runs/{run_id}/logs`) — cursor-paginated log fetch (`ListAgentLogsPage`), body in chronological order (oldest first). `?limit=` (default 200, cap 1000); omit `?before=` for the newest page (tail), or pass a prior `X-Prev-Cursor` to load earlier. When older entries remain, sets `X-Has-More: true` + `X-Prev-Cursor` (oldest id in the page). Fetches newest-first `limit+1`, trims, then reverses for the body. This is the "load earlier" path complementing the capped WS `agent.log_replay`.
- **SetArchived** (`PATCH /tasks/{id}/archive`) — toggles the `archived` flag; does not touch `label`. Archived tasks are hidden from the default list, skipped by ghsync, and never dispatched.
- **Bulk** (`POST /tasks/bulk`) — applies `move`/`pause`/`resume`/`archive`/`unarchive` to a list of ids; per-task results, `207` when any task fails. `move` goes through `engine.Transition` per task, guarded by the same `taskRunIsLive` check as `MoveLabel`/`Approve`/`Reject` (issue #333): a task with a live (`pending`/`running`) active agent run fails that one `bulkResult` with the same message `MoveLabel` returns as a `409` (`errRunLive`), rather than transitioning and clearing the lock — other ids in the batch are unaffected.
- **CancelRun** (`POST /tasks/{id}/runs/{run_id}/cancel`) — kill switch for an in-flight run. Validates the run belongs to the task and is `running`, then calls the injected `RunCanceller` (the agent pool, passed through `NewRouter` → `NewTasksHandler`). Returns `202` once cancellation is *signalled* — the pool marks the run `cancelled`, pauses the task, and broadcasts `task.agent_done` asynchronously — `409` if the run isn't running or is no longer registered in the pool, `404` if the run doesn't belong to the task. Decoupled from the pool via the `RunCanceller` interface so tests inject a fake; nil-safe (reports `409` when no pool is wired).
- **ReplyRun** (`POST /tasks/{id}/runs/{run_id}/reply`) — answers a `waiting_human` run's `request_human` question with text. Validates the run belongs to the task and is the task's *active* run, then calls the injected `ReplyDispatcher` (the agent dispatcher, passed through `NewRouter` → `NewTasksHandler`), which starts a continuation run (session-resumed for `claude`, cold with a `RESPONSE FROM HUMAN` prompt section otherwise). `202` + new run id; `400` empty message; `404` wrong task; `409` not the active waiting run / no config; `503` pool saturated. Nil-safe (reports `503` when no dispatcher is wired, e.g. in tests).
- **CreatePR** (`POST /tasks/{id}/pr`) — one-click PR creation: pushes the branch (from the worktree if present, else the main clone), resolves the repo's `forge.Forge` from its remote URL via `forge.ForRemote` (today only GitHub is registered, via `ghclient.GitHub{}`, which runs `gh pr create` under the hood; title from the task, body from `buildPRBody`). Idempotent — an existing PR for the branch is returned instead of erroring. The resulting URL + `git_state` are persisted via `SetTaskPR`. `400` for no branch / unsupported remote host, `502` when PR creation fails. Contrast with **PRURL** (`GET /tasks/{id}/pr-url`), which only builds a pre-filled forge compare URL (`Forge.CompareURL`) and needs no forge auth.

## Config-List Pagination

`GET /provider-configs` (`providers.go`), `GET /agents` (`agents.go`), `GET
/repos` (`repos.go`), and `GET /workflows` (`workflows.go`) all follow the
same cursor-pagination shape as `TasksHandler.List` above: `?limit=`
(default 200, cap 500 — see `defaultConfigPageLimit`/`maxConfigPageLimit` in
`tasks.go`) / `?after=<cursor>`, `X-Next-Cursor` on more pages, body stays a
plain array. Each is backed by a `...Page` sqlc query cursored on
`(created_at, id)`, mirroring `SearchTasksPage`. Two things to know:

- `agents.go`'s `List` pages `ListAllAgentConfigsPage` (all configs, enabled
  or not); the internal enabled-only lookup (`ListAgentConfigs`, used by
  dispatch/label-conflict checks) is deliberately left unpaginated.
- `workflows.go`'s `List` pages the raw `workflows` rows first via
  `ListWorkflowsPage`, then runs the per-row `buildResponse` (labels +
  transitions fan-out) only over the trimmed page — not over the full table.

The frontend's `api.workflows.list()` / `api.agents.list()` /
`api.providerConfigs.list()` / `api.repos.list()` still return a plain
`Promise<T[]>` to callers (dropdowns/stores assume the full list fits in
memory): `client.ts`'s `listAllPages()` helper loops on `X-Next-Cursor`,
concatenating every page, so no consumer had to change.

## Review Comments Handler Notes

Persistent inline diff review comments (`task_review_comments`). Open comments are injected into every agent run's prompt by the dispatcher; agents resolve them via the MCP sidecar's `resolve_comment` tool (applied by the pool on successful completion), and humans resolve/reopen via `PATCH /tasks/{id}/review-comments/{comment_id}`. Resolving an already-resolved comment returns `404` (the SQL guard is `status = 'open'`).

## Repos Handler Notes

When `repoBaseDir` is non-empty, both `Create` and `Update` reject paths outside that directory via the shared `withinBaseDir` helper, which resolves symlinks with `filepath.EvalSymlinks` (falling back to `filepath.Clean` if a path doesn't exist yet) before comparing — so a symlink under the base pointing outside it is rejected consistently on create and update. (The pre-clone check on a *derived* clone destination stays a lexical `filepath.Clean` comparison, since that path doesn't exist yet.)

Auto-clone (`POST /repos` with only a `remote_url`, no `path`) is **asynchronous**: the handler validates inputs, creates the repo row with `clone_status: cloning`, and returns `201` immediately, then `cloneRepoAsync` runs `git clone` in a background goroutine under a detached 30-minute context (not the request context — a large clone must not hit the server's 60s `WriteTimeout`). On success it marks the repo `ready` and records claude trust; on failure it removes the partial clone dir, marks the repo `error` with `clone_error`, and either way publishes a `repo.clone_done` / `repo.clone_failed` WS event (via the injected `RepoEventPublisher`, satisfied by `*ws.Hub`; nil in tests). An existing local `path` still verifies synchronously (`git rev-parse`) before persisting.

Issue sync (`issue_sync_enabled` / `issue_sync_label`): enabling requires both a `remote_url` and a `workflow_id` (Create returns `400` otherwise; Update validates the merged result). `PATCH` merges — omitted fields keep their existing values.

## Workflow YAML Handler Notes

`ExportWorkflowYAML` and `ImportWorkflowYAML` live in `workflow_yaml.go`. Import wraps all inserts in a single transaction to keep the workflow consistent on partial failure.

## Response Helpers

Defined in `respond.go`:

```go
JSON(w, status, v)     // marshal v as JSON with status
Err(w, status, msg)    // { "error": msg }
```

Every handler in this package routes errors through `Err`, so every
`/api/v1/*` response — success or failure — is JSON with a consistent shape.
This also holds outside this package for the two other pre-response-body
rejection points: `middleware.BearerAuth`'s 401 (inlines the same `{"error":
"unauthorized"}` shape rather than importing `handlers`, to avoid a risk of
import cycle) and `ws.ServeWS`'s pre-upgrade auth rejection (same shape, via
`encoding/json`). The one place that stays plain-text/no-body is a genuine
WebSocket upgrade failure inside `websocket.Accept` itself (after auth has
already passed) — at that point the library owns the response and there's
nothing meaningful to serialize; it's just logged.
