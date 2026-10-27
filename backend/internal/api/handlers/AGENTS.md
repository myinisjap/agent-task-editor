# internal/api/handlers

One file per resource group. All handlers receive a `*gen.Queries` for database access plus resource-specific dependencies.

## Files

| File | Handler | Key Dependencies |
|---|---|---|
| `tasks.go` | `TasksHandler` (CRUD, list/search, notes, label history, transitions) | `gen.Queries`, `workflow.Engine` |
| `task_response.go` | helpers for `TasksHandler` (wire-format wrapper + derived dependency/subtask/queue-position fields) | — |
| `task_uploads.go` | helper for `TasksHandler` (multipart attachment save; downscales oversized images via `image_resize.go`). Stored extension comes from the sniffed `image/*` type (`extForSniffedImageType`), never the client filename (ADR 0030) | — |
| `image_resize.go` | helper for `task_uploads.go` (downscales images exceeding 2000x2000px, preserving aspect ratio; GIF/WebP re-encoded to PNG when resized) | `golang.org/x/image/draw`, `golang.org/x/image/webp` |
| `uploads.go` | `UploadsHandler.ServeFile` — serves stored attachment files by task/filename (path-traversal-checked via `isSafePathComponent`). Sets `nosniff` + `Content-Disposition: inline` and `Content-Type` from `contentTypeForStoredExt`, not `http.ServeFile` inference (ADR 0030) | — |
| `task_bulk.go` | `TasksHandler` (pause/archive toggles + bulk action) | — |
| `task_runs.go` | `TasksHandler` (run list/get/logs/cancel/reply) | `agent` (error sentinels) |
| `task_pr.go` | `TasksHandler` (diff/pr/pr-url/github-status/git-state); Diff delegates to `reviewbrief` | `prflow`, `forge`, `reviewbrief` |
| `review_brief.go` | `ReviewBriefHandler` (GET /tasks/{id}/review-brief) | `reviewbrief`, `needsme` |
| `workflows.go` | `WorkflowsHandler` | `gen.Queries`, `*sql.DB` (for YAML import transactions) |
| `agents.go` | `AgentsHandler` | `gen.Queries` |
| `providers.go` | `ProviderConfigsHandler` — CRUD for Provider Configs (provider/model/env), the entity `agent_configs`/`chat_sessions` reference by `provider_config_id` | `gen.Queries` |
| `chat.go` | `ChatHandler` — interactive chat session CRUD + PTY terminal WebSocket upgrade, separate from the task/workflow state machine | `gen.Queries`, `Terminal` (`agent.TerminalManager`) |
| `repos.go` | `ReposHandler` | `gen.Queries`, `repoBaseDir string` |
| `review_comments.go` | `ReviewCommentsHandler` | `gen.Queries` |
| `templates.go` | `TemplatesHandler` | `gen.Queries` |
| `lessons.go` | `LessonsHandler` | `gen.Queries` |
| `dashboard.go` | `DashboardHandler` | `gen.Queries` |
| `health.go` | `Health` func | none |
| `health_providers.go` | `HealthHandler` | `gen.Queries`, mcp/repo config, backup dir/interval/keep |
| `backup.go` | `BackupHandler` | `*storage.DB` |
| `backup_settings.go` | `BackupSettingsHandler` | `gen.Queries` |
| `workflow_yaml.go` | helpers for `WorkflowsHandler` | — |

## Tasks Handler Notes

- **Approve** (`POST /tasks/{id}/approve`) follows the `success` human transition from the current label (`humanPathTarget`) via `engine.Transition`; `400` if none. **Reject** follows the `failure` human transition; optional `to_label` overrides; `400` if neither. **MoveLabel** (`PATCH /tasks/{id}/label`) is a human move validated by `engine.Transition` (board drag-and-drop).
- `Approve`, `Reject`, `MoveLabel` and bulk `move` call `rejectTransitionWhileRunLive` (`taskRunIsLive`/`runLiveStatuses`) first: `409` (a failed per-task `bulkResult` with `errRunLive` for bulk) when the active run is `pending`/`running`, because `engine.Transition` unconditionally clears the lock. Keep this guard on any new transition entry point. (ADR 0003)
- **List** (`GET /tasks`): `SearchTasksPage`; filters `q`, `label`, `repo_id`, `type`, `git_state`, tri-state `archived` (`''`/`only`/`all`, else `400`). Cursor pagination per ADR 0025 (limit default 200, cap 500; `X-Next-Cursor`; fetch `limit+1`).
- **ListRuns** (`task_runs.go`): same shape (limit default 100, cap 500). Lifetime cost is `Task.cumulative_cost_usd` (`SumTaskCost` in `Get`), not a sum over a page.
- **GetRunLogs**: `ListAgentLogsPage`, chronological body; limit default 200, cap 1000; omit `?before=` for the tail, else pass `X-Prev-Cursor`; sets `X-Has-More` + `X-Prev-Cursor` when older entries remain. Complements the capped WS `agent.log_replay`.
- **SetArchived** toggles `archived` only (hidden from default list, skipped by ghsync, never dispatched). **Bulk** (`POST /tasks/bulk`) applies `move`/`pause`/`resume`/`archive`/`unarchive` per id; `207` when any fails.
- **CancelRun** signals the injected `RunCanceller` (the pool): `202` once signalled, `409` not running/unregistered, `404` wrong task; nil-safe (`409`). The pool does the DB writes and broadcast (ADR 0012).
- **ReplyRun** answers a `waiting_human` run through the injected `ReplyDispatcher`: `202` + new run id; `400` empty message; `404` wrong task; `409` not the active waiting run / no config; `503` pool saturated or no dispatcher wired.
- **CreatePR** wraps `prflow.Service.CreateForTask` (shared with the engine `OnCreatePR` hook): pushes the branch (worktree if present, else main clone), resolves the `forge.Forge` via `forge.ForRemote` (GitHub via `ghclient.GitHub{}`), is idempotent (returns an existing PR), persists via `SetTaskPR`. `400` no branch / unsupported host, `502` creation failed. **PRURL** (`GET /tasks/{id}/pr-url`) only builds a compare URL (`Forge.CompareURL`), no auth.

## Config-List Pagination

`GET /provider-configs`, `/agents`, `/repos`, `/workflows` use the same cursor shape as `TasksHandler.List` (`defaultConfigPageLimit`/`maxConfigPageLimit` in `tasks.go`), each backed by a `...Page` query cursored on `(created_at, id)`. `agents.go` List pages `ListAllAgentConfigsPage` (enabled or not); the internal enabled-only `ListAgentConfigs` stays unpaginated. `workflows.go` List pages raw rows first, then runs `buildResponse` only over the page. The frontend's `listAllPages()` in `client.ts` concatenates pages so callers still get `T[]`.

## Review Comments Handler Notes

Open `task_review_comments` are injected into every run's prompt; agents resolve via the sidecar's `resolve_comment` (applied by the pool on success), humans via `PATCH /tasks/{id}/review-comments/{comment_id}`. Resolving an already-resolved comment returns `404` (SQL guard `status = 'open'`).

## Repos Handler Notes

- With `repoBaseDir` set, `Create` and `Update` both reject paths outside it via `withinBaseDir` (resolves symlinks with `filepath.EvalSymlinks`, falling back to `filepath.Clean`). The pre-clone check on a derived destination stays lexical.
- Auto-clone (`POST /repos` with only `remote_url`) is asynchronous: `201` with `clone_status: cloning`, then `cloneRepoAsync` in a goroutine under a detached 30-minute context (never the request context), marking `ready` or `error` (partial dir removed) and publishing `repo.clone_done`/`repo.clone_failed` via `RepoEventPublisher` (nil in tests). A local `path` verifies synchronously (`git rev-parse`).
- Issue sync: enabling requires `remote_url` and `workflow_id` (`400` otherwise; Update validates the merged result). `PATCH` merges. The handler names every column in `CreateRepoParams`/`UpdateRepoParams`, so pass real values (resolve `''` via `resolveIssueSyncUpdatePolicy`/`resolveIssueSyncGoneAction`); `repos_test.go` pins that an unrelated PATCH preserves the policy fields (ADR 0036).

## Workflow YAML Handler Notes

`ExportWorkflowYAML`/`ImportWorkflowYAML` live in `workflow_yaml.go`; import wraps all inserts in one transaction.

## Response Helpers

`respond.go`: `JSON(w, status, v)` and `Err(w, status, msg)` (`{"error": msg}`). Route every error through `Err`; the same shape is used by `middleware.BearerAuth` and `ws.ServeWS` pre-upgrade rejections. Only a failed `websocket.Accept` upgrade is plain (ADR 0030).
