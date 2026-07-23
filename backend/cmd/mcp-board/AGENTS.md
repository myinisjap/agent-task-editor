# cmd/mcp-board

A standing MCP (Model Context Protocol) server that lets a **chat client** (e.g.
Claude Desktop) manage the board — discover repos/workflows and create tickets.
Communication is over stdio using JSON-RPC 2.0 (newline-delimited JSON), the
same wire protocol as `cmd/mcp-server`.

## Why this is separate from cmd/mcp-server

`cmd/mcp-server` is the **per-run sidecar** the in-flow kanban agents get. It is
ephemeral (spawned as a child of each agent subprocess) and task-scoped (its
tools act on the one run via env-injected `RUN_ID`/`TRANSITIONS`/`RESULT_FILE`).

`cmd/mcp-board` is the opposite: a **long-lived process a human points a chat
client at**. It has no run context; it just talks to the backend REST API. The
flow agents never see its tools, so task creation lives here and *not* in the
flow agents' toolset — which is exactly the access boundary this binary exists
to enforce.

## Tools

- `list_repos()` — `GET /api/v1/repos`; returns id, name, workflow_id, clone_status
- `list_workflows()` — `GET /api/v1/workflows`; returns id, name, and label names
- `create_task(title, repo_id, [description], [type], [workflow_id], [label])` —
  `POST /api/v1/tasks`. Defaults `label` to `"work"` so a ticket is immediately
  agent-eligible; pass another label (e.g. `"not_ready"`) to stage it. When
  `workflow_id` is omitted, it is left out of the request entirely and the
  backend applies its own default workflow (the one named `"Default"`, else
  the alphabetically-first workflow — see `TasksHandler.resolveDefaultWorkflowID`)
  — it is **not** derived from the repo. Landing a task directly on a column is
  initial placement (see `TasksHandler.resolveInitialLabel`), so it is not
  restricted to the workflow's transition edges.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `BACKEND_URL` | ✅ | Base URL of the backend, e.g. `http://localhost:8080` |
| `API_TOKEN` | — | Bearer token; sent on every request when the backend requires one |
| `LOG_LEVEL` | — | `debug`/`info`/… (default `info`), matching the other binaries |

## Building & Connecting

```bash
cd backend && go build -o mcp-board ./cmd/mcp-board
```

Then register it with your chat client's MCP config, e.g. Claude Desktop's
`claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "task-editor-board": {
      "command": "/absolute/path/to/mcp-board",
      "env": { "BACKEND_URL": "http://localhost:8080", "API_TOKEN": "..." }
    }
  }
}
```

See `docs/board-mcp.md` for the full walkthrough.

## Protocol Details

- Only `initialize`, `tools/list`, and `tools/call` are implemented
- Notifications (no `id`) are ignored — no response sent
- Unknown methods return JSON-RPC error `-32601`
- Scanner buffer is 4 MB to match `cmd/mcp-server`
