# internal/config

Loads server configuration from a YAML file (path from `CONFIG_FILE` env var) with environment variables taking precedence over file values.

## Fields

| Struct Field | Env Var | YAML Key | Default |
|---|---|---|---|
| `DBPath` | `DB_PATH` | `db_path` | `agent-task-editor.db` |
| `Port` | `PORT` | `port` | `8080` |
| `CORSOrigins` | `CORS_ORIGINS` | `cors_origins` | `*` |
| `APIToken` | `API_TOKEN` | `api_token` | _(empty)_ |
| `MCPBinary` | `MCP_SERVER_PATH` | `mcp_server_path` | _(empty)_ |
| `LLMBaseURL` | `LLM_BASE_URL` | `llm_base_url` | `https://api.openai.com/v1` |
| `LLMAPIKey` | `LLM_API_KEY` | `llm_api_key` | _(empty)_ |
| `MaxWorkers` | `MAX_WORKERS` | `max_workers` | `5` |
| `RepoBaseDir` | `REPO_BASE_DIR` | `repo_base_dir` | _(empty)_ |
| `UploadDir` | `UPLOAD_DIR` | `upload_dir` | `uploads` (next to DB) |
| `GitHubSyncInterval` | `GITHUB_SYNC_INTERVAL` | `github_sync_interval` | `30s` |

## Loading Order

1. Start with `Defaults()`
2. If `CONFIG_FILE` is set and the file exists, unmarshal YAML over the defaults
3. Override any field that has a non-empty env var value

## Notes

- `REPO_BASE_DIR` empty is valid (warn-only at startup); production deployments should set it
- `APIToken` empty means no authentication is required
- `MCPBinary` empty means ClaudeRunner runs without MCP tools (`signal_complete`/`request_human` unavailable)
