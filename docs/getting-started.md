# Getting Started

## Prerequisites

- Docker and Docker Compose (recommended)
- **Or** Go 1.24+ and Node 20+ for local development
- An AI provider credential — see [Providers](providers/) for options

## Quick Start with Docker Compose

```bash
git clone https://github.com/myinisjap/agent-task-editor
cd agent-task-editor
./dev.sh start
```

Open `http://localhost:5173` in your browser.

The backend starts on `:8080`, the frontend nginx on `:5173`.

## `dev.sh` Commands

| Command | Description |
|---|---|
| `./dev.sh start` | Build and start all services with Docker Compose; prints board/API URLs |
| `./dev.sh stop` | Stop all Docker Compose services |
| `./dev.sh restart` | Stop and rebuild all services |
| `./dev.sh logs` | Tail backend logs from Docker |
| `./dev.sh login` | Run `claude login` inside the backend container (Claude auth setup) |
| `./dev.sh shell` | Open a shell inside the running backend container |
| `./dev.sh dev` | Start backend and frontend as local processes (no Docker); builds MCP server automatically |
| `./dev.sh dev-stop` | Kill any orphaned dev processes from `dev` mode |

## Environment Variables

All variables can also be set via a YAML config file pointed to by `CONFIG_FILE` (env vars always take precedence over file values).

### Core

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Backend HTTP port |
| `DB_PATH` | `agent-task-editor.db` | SQLite database file path |
| `API_TOKEN` | _(empty)_ | Bearer token for API auth; empty = no auth |
| `CORS_ORIGINS` | `*` | Comma-separated allowed origins (e.g. `http://localhost:5173`) |
| `MAX_WORKERS` | `5` | Maximum concurrent agent runs |
| `CONFIG_FILE` | _(empty)_ | Path to a YAML config file (all keys are optional) |

### Repository Access

| Variable | Default | Description |
|---|---|---|
| `REPO_BASE_DIR` | _(empty)_ | Restrict repo registration to paths under this directory. Supports `~/` prefix. If unset, any host path can be registered (not recommended in production). |
| `UPLOAD_DIR` | `uploads` (next to DB) | Directory for task attachment uploads |

### AI Providers

| Variable | Default | Description |
|---|---|---|
| `MCP_SERVER_PATH` | _(empty)_ | Path to the `mcp-server` binary. Required for MCP tools (`claude` and `qwen_code` providers). |
| `LLM_BASE_URL` | `https://api.openai.com/v1` | Base URL for the `llm` provider (any OpenAI-compat API) |
| `LLM_API_KEY` | _(empty)_ | API key for `llm` or `anthropic` provider |

### Other

| Variable | Default | Description |
|---|---|---|
| `GITHUB_SYNC_INTERVAL` | `30s` | How often to poll GitHub for PR status updates. Accepts Go duration strings (e.g. `1m`, `5m`). |
| `LOG_LEVEL` | `INFO` | Logging level: `DEBUG`, `INFO`, `WARN`, `ERROR` |

### YAML Config File

If `CONFIG_FILE` points to a YAML file, values from it are used as defaults (env vars override):

```yaml
port: "8080"
db_path: agent-task-editor.db
api_token: ""
cors_origins: "*"
mcp_server_path: /path/to/mcp-server
llm_base_url: https://api.openai.com/v1
llm_api_key: ""
max_workers: 5
repo_base_dir: /repos
upload_dir: /data/uploads
github_sync_interval: 30s
```

### Authentication

Set `API_TOKEN` to require an `Authorization: Bearer <token>` header on all API requests. WebSocket connections pass the token via `?token=<value>` since browsers cannot set custom headers on WebSocket upgrades.

The frontend reads `VITE_API_BASE_URL` and `VITE_WS_BASE_URL` at build time. For the Docker image these default to `""` (same origin). For local development add a `.env.local` in `frontend/`:

```
VITE_API_BASE_URL=http://localhost:8080
VITE_WS_BASE_URL=ws://localhost:8080
VITE_API_TOKEN=your-token-here
```

## Provider-Specific Setup

### Claude (`claude` provider)

The `claude` provider requires the Claude CLI binary and authentication. See [providers/claude.md](providers/claude.md) for full details.

The default `docker-compose.yml` mounts your host `~/.claude` directory:

```yaml
volumes:
  - ~/.claude:/root/.claude
  - /usr/local/bin/claude:/usr/local/bin/claude:ro
```

Run `./dev.sh login` to authenticate the CLI inside the container.

### Anthropic API (`anthropic` provider)

Set `LLM_API_KEY` to your Anthropic API key. No binary needed. See [providers/anthropic.md](providers/anthropic.md).

### OpenAI / LLM (`llm` provider)

Set `LLM_BASE_URL` and `LLM_API_KEY`. Works with any OpenAI-compatible API. See [providers/llm.md](providers/llm.md).

### Opencode (`opencode` provider)

Install the `opencode` binary and configure it. **MCP tools are not available.** See [providers/opencode.md](providers/opencode.md).

### Qwen Code (`qwen_code` provider)

Install the `qwen` binary. MCP tools are supported (same setup as `claude`). See [providers/qwen_code.md](providers/qwen_code.md).

## Mounting Repositories

Agents run with their working directory set to the registered repo path. Mount the directories you want agents to access:

```yaml
volumes:
  - /path/to/your/projects:/repos:rw
```

Then register repos using paths under `/repos` in the UI (or via the API), and set `REPO_BASE_DIR=/repos` to prevent agents from escaping that subtree.

## Local Development

### Backend

```bash
cd backend
go run ./cmd/server
```

Requires Go 1.24. The database file is created automatically with migrations applied on startup.

To regenerate SQL query code after editing `.sql` files:

```bash
cd backend
go generate ./...   # or: sqlc generate
```

Run tests:

```bash
go test ./...
```

### Frontend

```bash
cd frontend
npm install
npm run dev   # starts Vite dev server on :5173
```

### Building the MCP Sidecar

The MCP sidecar enables the `signal_complete`, `request_human`, `update_task_notes`, and `store_info` tools for the `claude` and `qwen_code` providers:

```bash
cd backend
go build -o mcp-server ./cmd/mcp-server
```

Set `MCP_SERVER_PATH=/path/to/mcp-server` in the backend environment. `./dev.sh dev` does this automatically.

## First Steps After Startup

1. **Register a repository** — go to Settings → Repos → Add Repo. Enter the local filesystem path of the repository agents should work in.
2. **Create an agent config** — go to Settings → Agents → New Agent. Select a provider, enter a model name, set target labels (e.g. `["todo", "in-progress"]`), and optionally write a system prompt.
3. **Create a task** — go to the Board, click New Task. Select the repo and fill in the title/description.
4. **Move it to `todo`** — drag it or use the label selector. The dispatcher will pick it up within 5 seconds and start an agent run.
5. **Watch the logs** — click on the task to open the detail view; live logs stream in real time.
