# Board MCP Server (create tickets from a chat)

`mcp-board` is an MCP (Model Context Protocol) server that lets you drive the
board from a chat: brainstorm a plan, then have it create tickets on the board.
It talks to the backend over the REST API.

There are two ways to use it:

1. **The in-app Chat tab** (recommended) — the backend wires these tools into the
   app's own chat sessions automatically. Just open a chat and ask it to create
   tickets. See [In-app chat tab](#in-app-chat-tab) below.
2. **An external chat client** such as Claude Desktop — run `mcp-board` yourself
   and point the client at it. See [External chat client](#external-chat-client).

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
`workflow_id`, and `clone_status`. Use it to find the `repo_id` to pass to
`create_task`.

### `list_workflows`
Lists the workflows and each workflow's label (column) names, so you can see
which labels a ticket can be created on (e.g. `work`, `not_ready`), and find
the `workflow_id` to pass to `create_task`.

### `create_task`
Creates a ticket on the board.

| Parameter | Required | Description |
|---|---|---|
| `title` | ✅ | Short title of the ticket |
| `repo_id` | ✅ | Repo the ticket belongs to (from `list_repos`) |
| `description` | — | What the ticket should accomplish (markdown) |
| `type` | — | `feature` (default) \| `bug` \| `chore` \| `spike` |
| `workflow_id` | — | Workflow the task is created under (from `list_workflows`). Defaults to the board's default workflow (the one named "Default", else the alphabetically-first workflow) when omitted — it is **not** derived from the repo. |
| `label` | — | Column the ticket starts on. **Defaults to `work`** so an agent picks it up immediately; pass e.g. `not_ready` to stage it for manual review first |

Landing a ticket directly on a column is *initial placement*, not a workflow
transition — so a ticket can be created straight on `work` even though the
workflow has no `not_ready → work` edge. The label must still be a real label in
the workflow, or the backend returns a 400.

> **Heads up:** every ticket created on `work` (or any agent-triggerable column)
> starts an agent run as soon as the dispatcher picks it up. If you want to
> review the batch first, create with `label: "not_ready"` and move them on the
> board when ready.

## In-app chat tab

The Docker images build `mcp-board` in and set `MCP_BOARD_PATH=/app/mcp-board`,
so the board tools are wired into the app's Chat tab out of the box. When you
open a chat session, the backend registers `mcp-board` with the session's CLI
(pointed at its own REST API), and the three tools become available in that
conversation.

To use it: open the **Chat** tab, start a session against the repo you want,
work through your plan, then ask it to create the tickets — it will call
`list_repos`/`create_task` and they appear on the board (on `work` by default).

Notes:
- This is a **human-driven** surface. It is deliberately separate from the
  in-flow kanban agents that process columns — those agents never get a
  task-creation tool. Only the chat you're talking to does.
- Provider support matches the task sidecar: `claude` and `qwen_code` (via a
  per-session `--mcp-config`), and `gemini_cli` / `codex_cli` (via a per-session
  home directory). `opencode` has no per-invocation MCP mechanism, so board
  tools aren't injected there.
- Running locally with `./dev.sh dev` builds `mcp-board` and sets
  `MCP_BOARD_PATH` automatically. If you run the server by hand, set
  `MCP_BOARD_PATH` to the built binary to enable it.
- Leaving `MCP_BOARD_PATH` unset simply launches chat sessions as before (no
  board tools).

## External chat client

To create tickets from a chat client outside the app (e.g. Claude Desktop), run
`mcp-board` yourself and register it:

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
