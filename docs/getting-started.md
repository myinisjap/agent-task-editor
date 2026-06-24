# Getting Started

## Prerequisites

- Docker and Docker Compose (recommended)
- **Or** Go 1.24+ and Node 20+ for local development
- A Claude Max subscription (for the `claude` provider) **or** an Anthropic API key (for the `anthropic` provider)

## Quick Start with Docker Compose

```bash
git clone https://github.com/myinisjap/agent-task-editor
cd agent-task-editor
docker compose up -d
```

Open `http://localhost:5173` in your browser.

The backend starts on `:8080`, the frontend nginx on `:5173`.

### Making the Claude CLI available to the container

The `claude` provider runs the `claude` CLI binary inside the backend container. The default `docker-compose.yml` mounts your host `~/.claude` directory so the container shares your existing Claude authentication:

```yaml
volumes:
  - ~/.claude:/root/.claude
```

Ensure the `claude` CLI is installed on your host and authenticated (`claude login`) before starting the stack. The CLI binary must also be reachable inside the container — add it to the image or mount the binary:

```yaml
volumes:
  - /usr/local/bin/claude:/usr/local/bin/claude:ro
  - ~/.claude:/root/.claude
```

### Mounting repositories

Agents run with their working directory set to the registered repo path. Mount the directories you want agents to access:

```yaml
volumes:
  - /path/to/your/projects:/repos:rw
```

Then register repos using paths under `/repos` in the UI (or via the API), and set `REPO_BASE_DIR=/repos` to prevent agents from escaping that subtree.

## Environment Variables

All variables can also be set via a YAML config file pointed to by `CONFIG_FILE`.

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Backend HTTP port |
| `DB_PATH` | `agent-task-editor.db` | SQLite database file path |
| `API_TOKEN` | _(empty)_ | Bearer token for API auth; empty = no auth |
| `CORS_ORIGINS` | `*` | Comma-separated allowed origins (e.g. `http://localhost:5173`) |
| `REPO_BASE_DIR` | _(empty)_ | Restrict repo registration to paths under this directory |
| `MCP_SERVER_PATH` | _(empty)_ | Path to the `mcp-server` binary; enables MCP tool calls for ClaudeRunner |
| `LLM_BASE_URL` | `https://api.openai.com/v1` | Base URL for the `llm` (OpenAI-compat) provider |
| `LLM_API_KEY` | _(empty)_ | API key for `llm` or `anthropic` provider |
| `MAX_WORKERS` | `5` | Maximum concurrent agent runs |

### Authentication

Set `API_TOKEN` to require a `Authorization: Bearer <token>` header on all API requests. WebSocket connections pass the token via `?token=<value>` since browsers cannot set custom headers on WebSocket upgrades.

The frontend reads `VITE_API_BASE_URL` and `VITE_WS_BASE_URL` at build time. For the Docker image these default to `""` (same origin). For local development add a `.env.local` in `frontend/`:

```
VITE_API_BASE_URL=http://localhost:8080
VITE_WS_BASE_URL=ws://localhost:8080
VITE_API_TOKEN=your-token-here
```

## Local Development

### Backend

```bash
cd backend
go run ./cmd/server
```

Requires Go 1.24. The database file (`agent-task-editor.db`) is created automatically with migrations applied on startup.

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

### Building the MCP sidecar

The MCP sidecar enables the `signal_complete` and `request_human` tools when using the `claude` provider:

```bash
cd backend
go build -o mcp-server ./cmd/mcp-server
```

Set `MCP_SERVER_PATH=/path/to/mcp-server` in the backend environment.

## First Steps After Startup

1. **Register a repository** — go to Settings → Repos → Add Repo. Enter the local filesystem path of the repository agents should work in.
2. **Create an agent config** — go to Settings → Agents → New Agent. Select a provider, enter a model name, set target labels (e.g. `["todo", "in-progress"]`), and optionally write a system prompt.
3. **Create a task** — go to the Board, click New Task. Select the repo and fill in the title/description.
4. **Move it to `todo`** — drag it or use the label selector. The dispatcher will pick it up within 5 seconds and start an agent run.
5. **Watch the logs** — click on the task to open the detail view; live logs stream in real time.
