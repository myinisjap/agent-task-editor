# Board MCP Server (create tickets from a chat)

`mcp-board` is a standalone MCP (Model Context Protocol) server that lets you
drive the board from a **chat client** such as Claude Desktop: brainstorm a plan
in the chat, then have it create tickets on the board. It talks to the backend
over the REST API, so it can run anywhere that can reach your backend URL.

## How it differs from the MCP sidecar

There are two MCP servers in this project, and they exist for opposite reasons:

| | `mcp-server` (sidecar) | `mcp-board` (this doc) |
|---|---|---|
| Lifecycle | Ephemeral — one per agent run | Long-lived — one per chat client |
| Launched by | The in-flow agent's CLI (`--mcp-config`) | You, via your chat client's MCP config |
| Scope | A single task/run | The whole board (via REST) |
| Tools | `signal_complete`, `request_human`, … | `list_repos`, `list_workflows`, `create_task` |
| Can create tasks? | No | Yes |

This split is deliberate: **the in-flow kanban agents never get a task-creation
tool.** Task creation lives only in `mcp-board`, which is a process *you* point a
chat client at — not something an agent processing a column can reach. See
[mcp-tools.md](mcp-tools.md) for the sidecar's tools.

## Tools

### `list_repos`
Lists the repositories configured on the board. Returns `id`, `name`,
`workflow_id`, and `clone_status`. Use it to find the `repo_id` (and default
workflow) to pass to `create_task`.

### `list_workflows`
Lists the workflows and each workflow's label (column) names, so you can see
which labels a ticket can be created on (e.g. `work`, `not_ready`).

### `create_task`
Creates a ticket on the board.

| Parameter | Required | Description |
|---|---|---|
| `title` | ✅ | Short title of the ticket |
| `repo_id` | ✅ | Repo the ticket belongs to (from `list_repos`) |
| `description` | — | What the ticket should accomplish (markdown) |
| `type` | — | `feature` (default) \| `bug` \| `chore` \| `spike` |
| `workflow_id` | — | Defaults to the repo's configured workflow |
| `label` | — | Column the ticket starts on. **Defaults to `work`** so an agent picks it up immediately; pass e.g. `not_ready` to stage it for manual review first |

Landing a ticket directly on a column is *initial placement*, not a workflow
transition — so a ticket can be created straight on `work` even though the
workflow has no `not_ready → work` edge. The label must still be a real label in
the workflow, or the backend returns a 400.

> **Heads up:** every ticket created on `work` (or any agent-triggerable column)
> starts an agent run as soon as the dispatcher picks it up. If you want to
> review the batch first, create with `label: "not_ready"` and move them on the
> board when ready.

## Setup

1. **Build it:**
   ```bash
   cd backend && go build -o mcp-board ./cmd/mcp-board
   ```

2. **Register it** with your chat client. For Claude Desktop, add to
   `claude_desktop_config.json`:
   ```json
   {
     "mcpServers": {
       "task-editor-board": {
         "command": "/absolute/path/to/mcp-board",
         "env": {
           "BACKEND_URL": "http://localhost:8080",
           "API_TOKEN": "your-token-if-set"
         }
       }
     }
   }
   ```

3. **Restart the chat client** and start a conversation: work through a plan,
   then ask it to create the tickets. It will call `list_repos` to find the repo
   and `create_task` for each ticket.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `BACKEND_URL` | ✅ | Base URL of the backend, e.g. `http://localhost:8080` |
| `API_TOKEN` | — | Bearer token; sent on every request when the backend has `API_TOKEN`/`API_TOKENS` set |
| `LOG_LEVEL` | — | `debug`/`info`/… (default `info`) |

## Security notes

- The server authenticates to the backend with `API_TOKEN`; give it a token with
  only the access you're comfortable exposing to the chat client.
- It never creates a task on a label that isn't defined in the target workflow.
- It is a REST client only — it holds no database access and runs no agents
  itself.
