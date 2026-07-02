# Agent Task Editor

A self-hosted Kanban board where AI agents automatically process tasks as they move through workflow columns. A label-based state machine governs task progression; the dispatcher picks up tasks in agent-triggerable states and runs the configured AI provider against the associated repository.

## Repository Layout

```
agent-task-editor/
├── docker-compose.yml        # Production deployment (backend :8080, frontend :5173)
├── openapi.yaml              # Full REST API spec
├── docs/                     # Feature and usage documentation
│   ├── overview.md
│   ├── getting-started.md
│   ├── workflows.md
│   ├── agents.md
│   ├── api.md
│   └── websocket.md
├── backend/                  # Go 1.24 server
│   ├── cmd/server/           # Main entrypoint
│   ├── cmd/mcp-server/       # MCP sidecar (signal_complete / request_human)
│   └── internal/
│       ├── agent/            # Provider system, pool, dispatcher
│       ├── api/              # Chi router, handlers, middleware
│       ├── config/           # YAML + env var config
│       ├── storage/          # SQLite, golang-migrate, sqlc-generated code
│       ├── tasksource/       # (task source abstraction)
│       ├── workflow/         # State machine engine
│       └── ws/               # WebSocket hub and client management
└── frontend/                 # React + TypeScript + Vite + Tailwind
    └── src/
        ├── api/              # REST client, WS client, shared types
        ├── components/       # Board, diff viewer, shared UI
        ├── pages/            # Board, TaskDetail, Dashboard, Workflow, AgentConfig
        └── stores/           # Zustand stores
```

## Quick Start

```bash
# Docker (recommended)
./dev.sh start   # or: docker compose up -d
# Open http://localhost:5173
# Note: on macOS, dev.sh automatically syncs Claude OAuth credentials from the
# macOS Keychain to ~/.claude/.credentials.json before starting, so the container
# can authenticate. Re-run dev.sh start after token expiry (~8 hours).

# Local dev (builds MCP server + backend + frontend, all in one)
./dev.sh dev
```

See `docs/getting-started.md` for full setup including Claude CLI auth and repo mounts.

## Key Environment Variables

| Variable | Default | Purpose |
|---|---|---|
| `API_TOKEN` | _(none)_ | Bearer token auth; empty = open |
| `REPO_BASE_DIR` | _(none)_ | Restrict repo paths; empty = any path allowed (warns on startup) |
| `MCP_SERVER_PATH` | _(none)_ | Path to mcp-server binary; enables signal_complete/request_human tools |
| `LLM_API_KEY` | _(none)_ | API key for `anthropic` or `llm` provider |
| `MAX_WORKERS` | `5` | Concurrent agent runs |
| `INSECURE_SKIP_SSL_VERIFY` | `false` | Set to `true` behind corporate TLS-inspecting proxies. Disables SSL verification for git clone, npm, and the claude CLI (Node.js). Set in your shell or a `.env` file — docker compose passes it as a build arg (npm install of claude-code) and runtime env vars (`GIT_SSL_NO_VERIFY`, `NPM_CONFIG_STRICT_SSL`, `NODE_TLS_REJECT_UNAUTHORIZED`). |

## Development Workflow

- **Backend code gen:** `cd backend && go generate ./...` (runs sqlc after editing `.sql` files)
- **Tests:** `cd backend && go test ./...`
- **Frontend types:** edit `frontend/src/api/types.ts` when adding new API fields
- **Migrations:** add numbered files to `backend/internal/storage/migrations/`; they run automatically on startup

## Common API Calls

All routes are under `/api/v1/`. Example base: `http://localhost:8080/api/v1`.

```bash
# List all tasks
curl http://localhost:8080/api/v1/tasks

# Get a specific task
curl http://localhost:8080/api/v1/tasks/<id>

# Filter by label
curl "http://localhost:8080/api/v1/tasks?label=work"

# Filter by repo
curl "http://localhost:8080/api/v1/tasks?repo_id=<id>"

# Move a task to a different label
curl -X PATCH http://localhost:8080/api/v1/tasks/<id>/label \
  -H "Content-Type: application/json" \
  -d '{"to_label": "review"}'

# Move with a note
curl -X PATCH http://localhost:8080/api/v1/tasks/<id>/label \
  -H "Content-Type: application/json" \
  -d '{"to_label": "done", "note": "shipped"}'

# List agent runs for a task
curl http://localhost:8080/api/v1/tasks/<id>/runs

# Get run logs
curl http://localhost:8080/api/v1/tasks/<id>/runs/<run_id>/logs
```

If `API_TOKEN` is set, add `-H "Authorization: Bearer <token>"` to every request.

## Architecture Decision Notes

- `active_agent_run_id` (set by dispatcher, cleared on completion) is separate from `current_agent_run_id` (last run ever associated with the task). The active field gates re-dispatch; the current field tracks what to replay on WS subscribe.
- `waiting_human` runs do NOT clear `active_agent_run_id` — this intentionally blocks re-dispatch until a human acts.
- `UpdateTaskLabel` always sets `active_agent_run_id = NULL` in SQL, so every transition implicitly unlocks dispatch.
- The MCP sidecar communicates via a temp file (`RESULT_FILE`) written at the end of a tool call, then read by the runner after the subprocess exits.
- `REPO_BASE_DIR` is optional (warn-only) to support development without it; production deployments should set it.
- Agent `Bash`/`run_bash` tool calls execute inside the **backend** container against bind-mounted repos, so any compiler/runtime an agent needs must be installed in `backend/Dockerfile`'s *final* image stage, not just available on the host. It currently ships Go 1.24 (copied from the Dockerfile's own builder stage for exact version parity with the server binary) and Node 22/npm (covers Vite/React/TS projects). To add another language, edit the final stage of `backend/Dockerfile` and rebuild — see `docs/getting-started.md#supported-languages--extending-the-toolchain`. `frontend/Dockerfile` is unrelated to this — it only builds/serves this app's own UI in production.
