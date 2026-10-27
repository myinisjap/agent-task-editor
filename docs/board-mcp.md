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

## Which surface gets which tools

Three surfaces serve the same board tools; each picks its set with a profile
(`create` = `list_repos`, `list_workflows`, `create_task`; `readonly`, `full`
and `admin` are the remote connector's sets, see the remote connector section below).

| | In-app Chat tab | Claude Desktop (stdio `mcp-board`) | Remote claude.ai connector |
|---|---|---|---|
| Transport | stdio, launched by the backend per chat session | stdio, launched by the chat client | MCP streamable HTTP at `<MCP_PUBLIC_URL>/mcp` |
| Auth | per-session scoped token (revoked when the session ends), never `API_TOKEN` | `API_TOKEN` in the client's `env` | Google sign-in + built-in OAuth (`MCP_ALLOWED_EMAILS`) |
| Profile env var | `MCP_BOARD_TOOL_PROFILE` (backend; passed on as `TOOL_PROFILE`) | `TOOL_PROFILE` (in the client's `env`) | `MCP_TOOL_PROFILE` (backend) |
| Default profile | `full` | `create` | `readonly` |
| Default tools | `full`: every `readonly` tool plus `create_task` and the task write tools (`reply_to_run`, `approve_task`, `reject_task`, `move_task`, `archive_task`, `cancel_run`, `rerun_task`, `create_pr`) | `list_repos`, `list_workflows`, `create_task` | `readonly`: the read tools only (`list_tasks`, `get_task`, `list_runs`, `get_run_logs`, `get_diff`, `list_needs_me`, `get_costs`, and the board-setup reads such as `get_workflow`, `list_agents`, `get_repo`, `list_templates`, `list_schedules`) |

The Chat tab defaults to `full` because it is driven by you, but it reads repo
code that can contain prompt injection; set `MCP_BOARD_TOOL_PROFILE=create` (or
`readonly`) to narrow it. `admin` adds board configuration (`create_workflow`,
`update_workflow`, `add_repo`, `update_repo`, `create_agent`, `update_agent`,
`create_template`, `update_template`, `create_schedule`, `update_schedule`) on
any surface that selects it. The full lists per profile are in
[Remote connector](#remote-connector).

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
(pointed at its own REST API), and the `full` profile's tools (see the table above) become available in that
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
- Chat sessions default to the `full` profile (the remote connector's read tools plus create_task, reply_to_run, approve_task, reject_task, move_task, archive_task). A chat reads repo code that can contain prompt injection, so set `MCP_BOARD_TOOL_PROFILE=create` (the three create tools) or `readonly` to narrow it, or `admin` to widen it. The value is passed to `mcp-board` as `TOOL_PROFILE`, and the session token is widened to the exact method+path pairs the profile's tools call (no DELETE, no other routes) unless the profile is `create`.

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

   Optionally add `"TOOL_PROFILE": "readonly"` (or `full`/`admin`) to `env` for
   the remote connector's tool sets; the default is the three create tools.

3. **Restart the chat client** and start a conversation: work through a plan,
   then ask it to create the tickets. It will call `list_repos` to find the repo
   and `create_task` for each ticket.

## Remote connector

> **Off by default, strictly opt-in.** Nothing here is active unless you do
> both of these: **(1)** set `MCP_REMOTE_ENABLED=true` plus the required
> `MCP_*` settings below (setting `MCP_PUBLIC_URL` and friends alone does
> *not* enable it, and `MCP_REMOTE_ENABLED=true` with a required setting
> missing fails startup), and **(2)** include the public-route override
> `docker-compose.mcp-remote.yml` when you use Traefik. `./run.sh` and
> `./dev.sh` do both with `--with-remote-mcp`; without that flag they force
> `MCP_REMOTE_ENABLED=false`. Without step 2 the backend feature can be on but
> no public, auth-free route exists. Startup logs whether remote MCP is on.

With `MCP_REMOTE_ENABLED=true`, the backend serves the board tools over the MCP
streamable-HTTP transport at `<MCP_PUBLIC_URL>/mcp`, so a remote client such as
a [claude.ai custom connector](https://support.claude.com/en/articles/11175166)
can use them. Remote clients can't send your static `API_TOKEN`, so the
endpoint runs its own small OAuth server that signs you in with Google and only
admits the emails in `MCP_ALLOWED_EMAILS`.

### Tools

`MCP_TOOL_PROFILE` picks one of three tool sets, each including the one before:

| Profile | Adds |
|---|---|
| `readonly` (default) | `list_repos`, `list_workflows` and the read tools below (`list_tasks` … `list_schedules`) |
| `full` | `create_task` and the task write tools (`reply_to_run`, `approve_task`, `reject_task`, `move_task`, `archive_task`, `cancel_run`, `rerun_task`, `create_pr`) |
| `admin` | Board configuration: `create_workflow`, `update_workflow`, `add_repo`, `update_repo`, `create_agent`, `update_agent`, and recurring work: `create_template`, `update_template`, `create_schedule`, `update_schedule` |

The full profile has everything the stdio server has, plus (read tools first):

| Tool | What it does |
|---|---|
| `list_tasks` | List tasks, filtered by `q`, `label`, `repo_id`, `type` (compact fields, newest first) |
| `get_task` | One task in full |
| `list_runs` | A task's agent runs; `waiting_human` means an agent is blocked on you |
| `get_run_logs` | The latest log entries of a run (defaults to the task's current run) |
| `get_diff` | The task branch's diff (capped at 100 KB) |
| `get_workflow` | One workflow as the YAML document the workflow editor's YAML tab shows |
| `list_agents` | Agent configs: which labels each works on, enabled, provider and model (no prompts or env) |
| `list_provider_configs` | Provider configs an agent can run on: id, name, provider, model (never their env) |
| `get_agent` | One agent config in full, including its system prompt (provider env stripped) |
| `get_repo` | One repo's settings, including `clone_status` / `clone_error` |
| `detect_repo_runtime` | Suggested toolchain pins from the repo's manifest files; changes nothing |
| `list_needs_me` | Tasks waiting on a human (task id, title, label, reason) — the "what needs me?" opener |
| `get_costs` | What agents have spent: total tokens/USD over finished runs, the split by provider, and the top tasks by cost (`limit`, default 5, max 50; or one `task_id`). The dashboard cost endpoints take no date/repo filters, so there are none |
| `list_templates` | Task templates (name, title, description, type) that schedules instantiate |
| `list_schedules` | Schedules: template, repo, cron, target label, enabled, last run |
| `reply_to_run` | Answer an agent waiting on a human (defaults to the task's active run) |
| `approve_task` / `reject_task` | Follow the workflow's `success` / `failure` human transition, with a note |
| `move_task` | Move a task to another label (must be an allowed transition) |
| `archive_task` | Archive a task (or unarchive it with `archived: false`); works on any label and removes the task's worktree |
| `cancel_run` | Stop a running agent run (defaults to the task's active run) |
| `rerun_task` | Clear the active run and failure count so an agent picks the task up again |
| `create_pr` | Push the task's branch and open a GitHub PR, returning its URL; idempotent (returns the existing PR). Needs a provisioned branch and a GitHub remote |

The admin profile adds:

| Tool | What it does |
|---|---|
| `create_workflow` | Create a workflow from a YAML document (same shape as [workflow export](workflows.md)) |
| `update_workflow` | Replace a workflow's name, description, labels and transitions from YAML. It's a full replacement, so read it with `get_workflow` first. Refused if it would drop a label that still holds tasks in that workflow (archived ones included) |
| `add_repo` | Onboard a repo: `remote_url` clones it under `REPO_BASE_DIR` in the background (poll `get_repo` for `clone_status: ready`), or `path` registers a checkout already on the server. Also takes `name`, `workflow_id`, `verify_command`, `verify_timeout_seconds`, `preview_command`, `preview_routes`, `preview_paths_glob`, `runtime_languages`, `max_concurrent_runs`, `issue_sync_enabled`, `issue_sync_label` |
| `update_repo` | Change those same settings (except `path`) on an existing repo; only the fields passed change |
| `create_agent` | Create an agent config: `name`, `provider_config_id` (from `list_provider_configs`), `system_prompt`, `labels`, `enabled`, `max_turns`, `timeout_secs`, `max_cost_usd`, `priority`, `effort`, `permission_mode`, `fallback_provider_config_ids`, `escalation_ladder`. Starts disabled if another agent already lists one of the labels, unless `enabled` is passed |
| `update_agent` | Change those fields on an existing agent; the current config is merged in, so only the fields passed change |
| `create_template` / `update_template` | Create a task template (`name`, `title`, `description`, `type`); an update changes only the fields passed |
| `create_schedule` | Create a task from a template in a repo on a 5-field cron (`minute hour day-of-month month day-of-week`, e.g. `0 9 * * 1` = Mondays 09:00). Optional `target_label`, `enabled` |
| `update_schedule` | Change `cron_expr` or `target_label`, or pause/resume with `enabled: false/true`; only the fields passed change. Template and repo are fixed. There are no delete tools |

A typical onboarding chat: `add_repo` → `get_repo` until ready →
`detect_repo_runtime` → `update_repo` with the pins and a `verify_command` →
`create_task`. Agents are matched to columns by label name, so a new workflow
gets runs on a label only if an enabled agent lists it (`list_agents`); wire one up
with `create_agent` / `update_agent`. Command allow/deny lists, plugins and MCP servers are not exposed and stay app-only; agents can have their system prompt rewritten from chat, so only grant `admin` to chats you trust.

Read-only tools are annotated `readOnlyHint: true`; the rest aren't, so clients
that confirm writes (claude.ai does by default) ask before creating, moving,
approving, rejecting, replying or changing configuration. `update_workflow` is
also annotated `destructiveHint: true`. Every task change is recorded in the
task's label history under the signed-in email, like a named `API_TOKENS` entry.

Provider config writes, settings, backup and delete endpoints, and agent deletion, are deliberately not
exposed, in any profile.

### Setup

1. **Google OAuth client.** In Google Cloud Console → APIs & Services →
   Credentials, use an existing *Web application* OAuth client (the one your
   reverse proxy's Google forward-auth already uses is fine) or create one, and
   add `<MCP_PUBLIC_URL>/oauth/callback` to its **Authorized redirect URIs**
   (e.g. `https://example.com/tasks/oauth/callback`).
2. **Backend env:**
   ```bash
   MCP_REMOTE_ENABLED=true                      # the explicit opt-in
   MCP_PUBLIC_URL=https://example.com/tasks   # where /mcp is reachable from the internet
   MCP_GOOGLE_CLIENT_ID=...apps.googleusercontent.com
   MCP_GOOGLE_CLIENT_SECRET=...
   MCP_ALLOWED_EMAILS=you@example.com           # comma-separated
   MCP_SIGNING_KEY=...                          # openssl rand -base64 48 (>= 32 chars)
   # MCP_TOOL_PROFILE=full                      # default is read-only; admin adds board setup; see below
   ```
   With the bundled nginx (`frontend/nginx.conf`) the app lives under `/tasks`,
   so `MCP_PUBLIC_URL` ends in `/tasks`. If you expose the backend directly,
   use its own base URL.
3. **Reverse proxy.** These paths must reach the backend *without* any
   login wall in front (claude.ai calls them from its servers, not your
   browser): `/tasks/mcp`, `/tasks/oauth/*`,
   `/.well-known/oauth-protected-resource*` and
   `/.well-known/oauth-authorization-server*`. The bundled nginx forwards them
   to the backend. The opt-in override `docker-compose.mcp-remote.yml` (not
   part of `docker-compose.traefik.yml`) adds a second Traefik router
   for exactly these paths, without the `forward-auth` middleware, that goes
   straight to the backend (stripping `/tasks`) rather than through nginx, so
   it can't reach any other part of the app. The rest of the UI stays behind
   forward-auth. Keep `API_TOKEN` set anyway: it's what protects the API if a
   proxy rule is ever wrong.

   Include the override explicitly:
   `docker compose -f docker-compose.yml -f docker-compose.traefik.yml -f docker-compose.mcp-remote.yml up -d`
   (or `./run.sh --with-remote-mcp` / `./dev.sh --with-remote-mcp`, which need
   `TRAEFIK_HOST` set). Without the file, no auth-free route exists.

   The two `/.well-known/` paths sit at the host root. If another app on the
   same host serves its own OAuth metadata there, give this app its own host.
4. **Connect.** In claude.ai → Settings → Connectors → *Add custom connector*,
   enter `<MCP_PUBLIC_URL>/mcp` and leave the OAuth fields empty (the connector
   registers itself). Sign in with an allowed Google account when prompted. In a
   Project, also enable the connector in the project's settings; conversations
   started after that get the tools.

### How it's secured

- `/mcp` requires a bearer access token issued by this server. Tokens are
  HMAC-signed with `MCP_SIGNING_KEY` (required, at least 32 characters, separate
  from the Google client secret; the server refuses to start without it),
  bound to the `/mcp` URL, and valid for 1 hour; refresh tokens last 30 days.
  Rotating `MCP_SIGNING_KEY` revokes every token and registered client at once.
- The email is re-checked against `MCP_ALLOWED_EMAILS` on every request and
  refresh, so removing an address cuts it off.
- Each sign-in is stored as a revocable grant in the `mcp_grants` table, and its
  id is carried by every token minted for it. See [Revoking access](#revoking-access).
- OAuth redirects are pinned to an exact set of URIs, `MCP_REDIRECT_URIS`
  (default: claude.ai's and claude.com's `/api/mcp/auth_callback`). A different
  path, host or loopback address is refused at registration and again at every
  redirect. PKCE (S256) is required. Because dynamic registration is open to
  anyone who can reach `/oauth/register`, you can additionally pin your own
  client: connect once, copy the `client_id` the server logs
  (`remote MCP: registered OAuth client`), set `MCP_ALLOWED_CLIENT_IDS` to it and
  restart; other clients are then refused at `/oauth/authorize` and `/oauth/token`.
  The client id stays valid as long as `MCP_SIGNING_KEY` is unchanged.
- Tool profile: by default (`MCP_TOOL_PROFILE=readonly`) only the read tools
  (`list_repos`, `list_workflows`, `list_tasks`, `get_task`, `list_runs`,
  `get_run_logs`, `get_diff`, `get_workflow`, `list_agents`, `get_repo`,
  `detect_repo_runtime`, `list_needs_me`, `get_costs`, `list_provider_configs`, `get_agent`, `list_templates`, `list_schedules`) are exposed, because run logs, diffs and task
  bodies contain text an attacker can influence and could steer a model into
  calling write tools. `MCP_TOOL_PROFILE=full` also exposes `create_task`,
  `reply_to_run`, `approve_task`, `reject_task`, `move_task`, `archive_task`, `cancel_run`, `rerun_task` and `create_pr`; if you enable
  it, keep claude.ai's per-tool "ask before running" and avoid "always allow"
  on those tools. `MCP_TOOL_PROFILE=admin` further exposes workflow editing, agent configs,
  repo onboarding and recurring work. A repo's `verify_command` runs as a shell command on the
  server (as the agent user), so only pick `admin` if you would let the chat
  run commands there, and never "always allow" `add_repo`, `update_repo`, `create_agent` or `update_agent` (an agent's system prompt steers what it runs). A schedule creates tasks
  unattended: with an empty `target_label` they wait in the human-gate column
  for you; pointing it at a label an agent works on starts agent runs on the
  cron with nobody watching, so never "always allow" `create_schedule` or
  `update_schedule` either.
  The consent page states which profile is active.
- Tool calls reach the API in-process under a trusted-actor mark that only the
  `/api/v1` auth chain honours; `/metrics` and every other route ignore it.
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

### Revoking access

Remove an email from `MCP_ALLOWED_EMAILS` to cut an account off, or revoke
individual sign-ins with the server binary (run it against the same database):

```bash
docker compose exec backend /app/server mcp-grants list
docker compose exec backend /app/server mcp-grants revoke <grant-id>
docker compose exec backend /app/server mcp-grants revoke-email you@example.com
```

A revoked grant's access and refresh tokens stop working immediately; other
sign-ins are unaffected and the user can simply connect again.

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
| `TOOL_PROFILE` | — | `create` (default when unset: list_repos/list_workflows/create_task), `readonly`, `full` or `admin` — the remote connector's tool sets; unknown values exit with an error |
| `LOG_LEVEL` | — | `debug`/`info`/… (default `info`) |

These are for the stdio `mcp-board` binary. The remote connector is configured
on the backend itself:

| Variable | Required | Description |
|---|---|---|
| `MCP_REMOTE_ENABLED` | ✅ to enable | Explicit opt-in, default `false`. Other `MCP_*` values alone never enable the endpoint; `true` with a required value missing fails startup |
| `MCP_PUBLIC_URL` | ✅ | Public base URL `/mcp` and `/oauth/*` are served under, e.g. `https://example.com/tasks` |
| `MCP_GOOGLE_CLIENT_ID` | ✅ | Google OAuth client ID |
| `MCP_GOOGLE_CLIENT_SECRET` | ✅ | Google OAuth client secret |
| `MCP_SIGNING_KEY` | ✅ | Token-signing key, at least 32 characters (`openssl rand -base64 48`). Independent of the Google secret |
| `MCP_ALLOWED_EMAILS` | ✅ | Comma-separated Google accounts allowed to connect |
| `MCP_TOOL_PROFILE` | — | `readonly` (default), `full` or `admin`; see [How it's secured](#how-its-secured) |
| `MCP_REDIRECT_URIS` | — | Exact OAuth redirect URIs allowed (default `https://claude.ai/api/mcp/auth_callback,https://claude.com/api/mcp/auth_callback`) |
| `MCP_ALLOWED_CLIENT_IDS` | — | Comma-separated OAuth client ids to accept; empty = any client with an allowed redirect |

After connecting, you'll see the consent page once per sign-in; claude.ai
refreshes tokens on its own after that.

## Security notes

- The server authenticates to the backend with `API_TOKEN`; give it a token with
  only the access you're comfortable exposing to the chat client.
- It never creates a task on a label that isn't defined in the target workflow.
- It is a REST client only — it holds no database access and runs no agents
  itself.
