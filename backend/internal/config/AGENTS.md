# internal/config

Loads server configuration from a YAML file (path from `CONFIG_FILE` env var) with environment variables taking precedence over file values.

## Fields

| Struct Field | Env Var | YAML Key | Default |
|---|---|---|---|
| `DBPath` | `DB_PATH` | `db_path` | `agent-task-editor.db` |
| `Port` | `PORT` | `port` | `8080` |
| `CORSOrigins` | `CORS_ORIGINS` | `cors_origins` | `http://localhost:5173,http://localhost:8080` |
| `APIToken` | `API_TOKEN` | `api_token` | _(empty)_ |
| `APITokens` | `API_TOKENS` | `api_tokens` | _(empty map)_ |
| `MetricsToken` | `METRICS_TOKEN` | `metrics_token` | _(empty)_ |
| `MCPBinary` | `MCP_SERVER_PATH` | `mcp_server_path` | _(empty)_ |
| `MCPBoardBinary` | `MCP_BOARD_PATH` | `mcp_board_path` | _(empty)_ |
| `LLMBaseURL` | `LLM_BASE_URL` | `llm_base_url` | `https://api.openai.com/v1` |
| `LLMAPIKey` | `LLM_API_KEY` | `llm_api_key` | _(empty)_ |
| `MaxWorkers` | `MAX_WORKERS` | `max_workers` | `5` |
| `RepoBaseDir` | `REPO_BASE_DIR` | `repo_base_dir` | _(empty)_ |
| `UploadDir` | `UPLOAD_DIR` | `upload_dir` | `uploads` (next to DB) |
| `GitHubSyncInterval` | `GITHUB_SYNC_INTERVAL` | `github_sync_interval` | `30s` |
| `IssueSyncInterval` | `ISSUE_SYNC_INTERVAL` | `issue_sync_interval` | `60s` |
| `BackupDir` | `BACKUP_DIR` | `backup_dir` | _(empty, scheduler disabled)_ |
| `BackupInterval` | `BACKUP_INTERVAL` | `backup_interval` | `24h` (initial/seed value only — see below) |
| `BackupKeep` | `BACKUP_KEEP` | `backup_keep` | `7` (initial/seed value only — see below) |

`BackupInterval`/`BackupKeep` only seed the `backup_settings` DB row on first
migration; once running, both are edited at runtime via
`PUT /api/v1/backup/settings` (10-minute minimum interval), independent of
this config/env-var layer. `BackupDir` remains a restart-required,
config/env-var-only choice — see `internal/backup` and `docs/backup.md`.

## Loading Order

1. Start with `Defaults()`
2. If `CONFIG_FILE` is set and the file exists, unmarshal YAML over the defaults
3. Override any field that has a non-empty env var value

## Notes

- `REPO_BASE_DIR` empty is valid (warn-only at startup); production deployments should set it
- `APIToken` empty means no authentication is required
- `APITokens` is `name -> token`. Unlike `APIToken`, a match resolves to an actor
  name (via `middleware.ActorFromContext`) recorded in `task_label_history.actor_id`
  for human-triggered transitions. `API_TOKENS` env var format is
  `name1:token1,name2:token2` (comma-separated pairs, split on the first `:`,
  whitespace trimmed); malformed entries are skipped with a `slog.Warn`. Env var
  entries are merged into (and override same-named) YAML `api_tokens` entries.
  `APIToken` is still supported as an anonymous/legacy fallback — a request
  authenticated with it resolves to actor `""`, same as before this field existed.
- `MetricsToken` empty (the default) leaves `GET /metrics` unauthenticated, independent of `APIToken`/`APITokens` — most Prometheus scrape setups can't easily carry a different token than other tooling.
- `MCPBinary` empty means ClaudeRunner runs without MCP tools (`signal_complete`/`request_human` unavailable)
- `MCPBoardBinary` empty means chat sessions launch without the board MCP tools (`list_repos`/`list_workflows`/`create_task`); set it to the `mcp-board` binary path to let chat sessions create tickets. See `docs/board-mcp.md`.
- `BackupDir` empty disables the automatic local-snapshot scheduler (`internal/backup.Scheduler`); the on-demand `GET /api/v1/backup` endpoint and the Health page's "Download backup" button are always available regardless. See `docs/backup.md`.
- `CORSOrigins` defaults to the known local dev/prod origins (`http://localhost:5173,http://localhost:8080`), not `*`; the wildcard is still available by setting `CORS_ORIGINS=*` explicitly. An empty `APIToken` now also triggers a startup `slog.Warn` in `cmd/server/main.go` (warn-only, matching the `REPO_BASE_DIR` pattern), escalated to a stronger message when `CORSOrigins == "*"` as well.
