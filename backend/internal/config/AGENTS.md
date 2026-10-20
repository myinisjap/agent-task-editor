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
| `MetricsPublic` | `METRICS_PUBLIC` | `metrics_public` | `false` |
| `MCPBinary` | `MCP_SERVER_PATH` | `mcp_server_path` | _(empty)_ |
| `MCPBoardBinary` | `MCP_BOARD_PATH` | `mcp_board_path` | _(empty)_ |
| `MCPBoardToolProfile` | `MCP_BOARD_TOOL_PROFILE` | `mcp_board_tool_profile` | _(empty = create)_ |
| `MaxWorkers` | `MAX_WORKERS` | `max_workers` | `5` |
| `RepoBaseDir` | `REPO_BASE_DIR` | `repo_base_dir` | _(empty)_ |
| `UploadDir` | `UPLOAD_DIR` | `upload_dir` | `uploads` (next to DB) |
| `GitHubWebhookSecret` | `GITHUB_WEBHOOK_SECRET` | `github_webhook_secret` | _(empty = webhook disabled)_ |
| `GitHubSyncInterval` | `GITHUB_SYNC_INTERVAL` | `github_sync_interval` | `30s` |
| `IssueSyncInterval` | `ISSUE_SYNC_INTERVAL` | `issue_sync_interval` | `60s` |
| `BackupDir` | `BACKUP_DIR` | `backup_dir` | _(empty, scheduler disabled)_ |
| `BackupInterval` | `BACKUP_INTERVAL` | `backup_interval` | `24h` (initial/seed value only — see below) |
| `BackupKeep` | `BACKUP_KEEP` | `backup_keep` | `7` (initial/seed value only — see below) |

## Loading Order

1. Start with `Defaults()`
2. If `CONFIG_FILE` is set and the file exists, unmarshal YAML over the defaults
3. Override any field that has a non-empty env var value

## Notes

- `REPO_BASE_DIR` empty is valid (warn-only at startup); an empty `API_TOKEN`/`API_TOKENS` also warns. Keep new safety config permissive-by-default with a startup warning (ADR 0005).
- `APITokens` is `name -> token`; a match resolves to an actor name (`middleware.ActorFromContext`) recorded in `task_label_history.actor_id`. `API_TOKENS` format: `name1:token1,name2:token2` (split on the first `:`, whitespace trimmed, malformed entries skipped with `slog.Warn`); env entries override same-named YAML entries. The legacy `APIToken` resolves to actor `""`.
- `/metrics` auth: `MetricsToken` set → that token only; else if `APIToken`/`APITokens` configured → an API token is required; `MetricsPublic=true` opts out; no API auth → open.
- `MCPBinary` empty: runners run without the sidecar tools. `MCPBoardBinary` empty: chat sessions have no board tools (`docs/board-mcp.md`).
- `BackupDir` empty disables the scheduler (restart required to change); on-demand `GET /api/v1/backup` is always available (`docs/backup.md`). `BackupInterval`/`BackupKeep` only seed the DB row; runtime changes go through `PUT /api/v1/backup/settings` (10-minute minimum) — ADR 0031.
- `CORSOrigins` defaults to the local dev/prod origins, not `*`; `CORS_ORIGINS=*` must be explicit.
