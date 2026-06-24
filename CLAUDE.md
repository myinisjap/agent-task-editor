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
docker compose up -d
# Open http://localhost:5173

# Local dev
cd backend && go run ./cmd/server   # :8080
cd frontend && npm install && npm run dev  # :5173
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

## Development Workflow

- **Backend code gen:** `cd backend && go generate ./...` (runs sqlc after editing `.sql` files)
- **Tests:** `cd backend && go test ./...`
- **Frontend types:** edit `frontend/src/api/types.ts` when adding new API fields
- **Migrations:** add numbered files to `backend/internal/storage/migrations/`; they run automatically on startup

## Architecture Decision Notes

- `active_agent_run_id` (set by dispatcher, cleared on completion) is separate from `current_agent_run_id` (last run ever associated with the task). The active field gates re-dispatch; the current field tracks what to replay on WS subscribe.
- `waiting_human` runs do NOT clear `active_agent_run_id` — this intentionally blocks re-dispatch until a human acts.
- `UpdateTaskLabel` always sets `active_agent_run_id = NULL` in SQL, so every transition implicitly unlocks dispatch.
- The MCP sidecar communicates via a temp file (`RESULT_FILE`) written at the end of a tool call, then read by the runner after the subprocess exits.
- `REPO_BASE_DIR` is optional (warn-only) to support development without it; production deployments should set it.
