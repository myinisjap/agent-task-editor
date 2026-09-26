# Board MCP Server (create tickets from a chat)

`mcp-board` is an MCP (Model Context Protocol) server that lets you drive the
board from a chat: brainstorm a plan, then have it create tickets on the board.
It talks to the backend over the REST API.

There are three ways to use it:

1. **The in-app Chat tab** (recommended) — the backend wires these tools into the
   app's own chat sessions automatically. Just open a chat and ask it to create
   tickets. See [In-app chat tab](#in-app-chat-tab) below.
2. **An external chat client** such as Claude Desktop — run `mcp-board` yourself
   and point the client at it. See [External chat client](#external-chat-client).
3. **A remote connector** such as a claude.ai custom connector — the backend
   serves the tools over HTTP at `/mcp`, behind a Google sign-in. This surface
   also has tools for reading tasks and runs and for unblocking agents. See
   [Remote connector](#remote-connector).

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
  per-session `--mcp-config`), and `codex_cli` (via a per-session
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

## Remote connector

With `MCP_PUBLIC_URL` set, the backend serves the board tools over the MCP
streamable-HTTP transport at `<MCP_PUBLIC_URL>/mcp`, so a remote client such as
a [claude.ai custom connector](https://support.claude.com/en/articles/11175166)
can use them. Remote clients can't send your static `API_TOKEN`, so the
endpoint runs its own small OAuth server that signs you in with Google and only
admits the emails in `MCP_ALLOWED_EMAILS`.

### Tools

Everything the stdio server has, plus:

| Tool | What it does |
|---|---|
| `list_tasks` | List tasks, filtered by `q`, `label`, `repo_id`, `type` (compact fields, newest first) |
| `get_task` | One task in full |
| `list_runs` | A task's agent runs; `waiting_human` means an agent is blocked on you |
| `get_run_logs` | The latest log entries of a run (defaults to the task's current run) |
| `get_diff` | The task branch's diff (capped at 100 KB) |
| `reply_to_run` | Answer an agent waiting on a human (defaults to the task's active run) |
| `approve_task` / `reject_task` | Follow the workflow's `success` / `failure` human transition, with a note |
| `move_task` | Move a task to another label (must be an allowed transition) |

Read-only tools are annotated `readOnlyHint: true`; the rest aren't, so clients
that confirm writes (claude.ai does by default) ask before creating, moving,
approving, rejecting or replying. Every change is recorded in the task's label
history under the signed-in email, like a named `API_TOKENS` entry.

Workflow, agent, provider, settings, backup and delete endpoints are
deliberately not exposed.

### Setup

1. **Google OAuth client.** In Google Cloud Console → APIs & Services →
   Credentials, use an existing *Web application* OAuth client (the one your
   reverse proxy's Google forward-auth already uses is fine) or create one, and
   add `<MCP_PUBLIC_URL>/oauth/callback` to its **Authorized redirect URIs**
   (e.g. `https://example.com/tasks/oauth/callback`).
2. **Backend env:**
   ```bash
   MCP_PUBLIC_URL=https://example.com/tasks   # where /mcp is reachable from the internet
   MCP_GOOGLE_CLIENT_ID=...apps.googleusercontent.com
   MCP_GOOGLE_CLIENT_SECRET=...
   MCP_ALLOWED_EMAILS=you@example.com           # comma-separated
   ```
   With the bundled nginx (`frontend/nginx.conf`) the app lives under `/tasks`,
   so `MCP_PUBLIC_URL` ends in `/tasks`. If you expose the backend directly,
   use its own base URL.
3. **Reverse proxy.** These paths must reach the backend *without* any
   login wall in front (claude.ai calls them from its servers, not your
   browser): `/tasks/mcp`, `/tasks/oauth/*`,
   `/.well-known/oauth-protected-resource*` and
   `/.well-known/oauth-authorization-server*`. The bundled nginx forwards them
   to the backend. `docker-compose.traefik.yml` adds a second Traefik router
   for exactly these paths, without the `forward-auth` middleware, that goes
   straight to the backend (stripping `/tasks`) rather than through nginx, so
   it can't reach any other part of the app. The rest of the UI stays behind
   forward-auth. Keep `API_TOKEN` set anyway: it's what protects the API if a
   proxy rule is ever wrong.

   The two `/.well-known/` paths sit at the host root. If another app on the
   same host serves its own OAuth metadata there, give this app its own host.
4. **Connect.** In claude.ai → Settings → Connectors → *Add custom connector*,
   enter `<MCP_PUBLIC_URL>/mcp` and leave the OAuth fields empty (the connector
   registers itself). Sign in with an allowed Google account when prompted. In a
   Project, also enable the connector in the project's settings; conversations
   started after that get the tools.

### How it's secured

- `/mcp` requires a bearer access token issued by this server. Tokens are
  HMAC-signed with a key derived from `MCP_GOOGLE_CLIENT_SECRET`, bound to the
  `/mcp` URL, and valid for 1 hour; refresh tokens last 30 days.
- The email is re-checked against `MCP_ALLOWED_EMAILS` on every request and
  refresh, so removing an address cuts it off. Rotating the Google client
  secret revokes every token at once.
- Dynamic client registration only accepts https redirect URIs on
  `MCP_REDIRECT_HOSTS` (default `claude.ai,claude.com`) or loopback, and the
  host is re-checked on every redirect. PKCE (S256) is required.
- Before sending you to Google, the server shows a consent page naming the
  client and where access will be sent. Only continue if you just clicked
  Connect yourself.
- Each sign-in is bound to the browser that started it by a short-lived,
  HttpOnly cookie, so a Google sign-in link someone else generated can't be
  completed in your browser.
- At most 1000 sign-ins can be in progress at once, which bounds the memory
  anonymous callers can use.
- Tool calls run in-process against the same REST handlers as the UI, so all
  the usual validation (allowed transitions, labels, run state) applies.
- Sign-ins in progress and unredeemed codes are held in memory: a restart
  mid-sign-in just means clicking Connect again. Issued tokens survive restarts.

### Getting told when something changes

MCP can't wake a chat: the client only calls the server while it's answering
you. To hear about work that needs you, point `NOTIFY_WEBHOOK_URL` at a push
service (ntfy, Slack, Discord, …) — see
[websocket.md#outbound-webhook](websocket.md#outbound-webhook) — and ask Claude to follow up on the task. When
agents open PRs, a Claude session that watches those PRs on GitHub will also be
woken by CI and review activity.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `BACKEND_URL` | ✅ | Base URL of the backend, e.g. `http://localhost:8080` |
| `API_TOKEN` | — | Bearer token; sent on every request when the backend has `API_TOKEN`/`API_TOKENS` set |
| `LOG_LEVEL` | — | `debug`/`info`/… (default `info`) |

These are for the stdio `mcp-board` binary. The remote connector is configured
on the backend itself:

| Variable | Required | Description |
|---|---|---|
| `MCP_PUBLIC_URL` | ✅ to enable | Public base URL `/mcp` and `/oauth/*` are served under, e.g. `https://example.com/tasks`. Unset = remote endpoint off |
| `MCP_GOOGLE_CLIENT_ID` | ✅ | Google OAuth client ID |
| `MCP_GOOGLE_CLIENT_SECRET` | ✅ | Google OAuth client secret (also seeds the token-signing key) |
| `MCP_ALLOWED_EMAILS` | ✅ | Comma-separated Google accounts allowed to connect |
| `MCP_REDIRECT_HOSTS` | — | Hosts OAuth clients may register https redirects on (default `claude.ai,claude.com`) |

After connecting, you'll see the consent page once per sign-in; claude.ai
refreshes tokens on its own after that.

## Security notes

- The server authenticates to the backend with `API_TOKEN`; give it a token with
  only the access you're comfortable exposing to the chat client.
- It never creates a task on a label that isn't defined in the target workflow.
- It is a REST client only — it holds no database access and runs no agents
  itself.
