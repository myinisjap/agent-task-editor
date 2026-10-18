# Getting Started

## Security Notice

> **Agent Task Editor executes arbitrary shell commands.** AI agents run with full shell access, as a separate unprivileged `agent` user inside the container (see [Agent user isolation](#agent-user-isolation)), but that user can still read and write your mounted repos and `~/.claude`. The defaults are permissive and intended for localhost use only.
>
> For any non-localhost deployment, set `API_TOKEN`, `REPO_BASE_DIR`, and a tight `CORS_ORIGINS` before starting the stack. Run behind a reverse proxy or VPN. See the [Security section in the README](../README.md#security) for the full checklist.

## Prerequisites

- Docker and Docker Compose (recommended)
- **Or** Go 1.25+ and Node 20+ for local development
- An AI provider credential — see [Providers](providers/) for options

## Quick Start with Docker Compose

```bash
git clone https://github.com/myinisjap/agent-task-editor
cd agent-task-editor
./dev.sh start
```

Open `http://localhost:5173` in your browser.

The backend starts on `:8080`, the frontend nginx on `:5173`. Both ports are published on
`127.0.0.1` by default (see `ATE_BIND_ADDR` below) — for anyone else on your network to reach
the board, you need to opt in.

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

## Deploying Behind Traefik

A `docker-compose.traefik.yml` override is included for running the app behind a Traefik reverse proxy. It removes the host port bindings (so `ATE_BIND_ADDR` — see [Environment Variables](#environment-variables) — has no effect once this override is in play) and adds the Traefik labels needed to route traffic.

Create a `.env` file in the project root (gitignored) with your hostname:

```
TRAEFIK_HOST=your.domain.com
```

Then start with both compose files:

```bash
docker compose -f docker-compose.yml -f docker-compose.traefik.yml up -d --build
```

Or use `dev.sh` — it picks up the override automatically when `TRAEFIK_HOST` is set:

```bash
TRAEFIK_HOST=your.domain.com ./dev.sh start
# or export it / set it in .env first, then:
./dev.sh start
```

The app is served at `https://your.domain.com/tasks`. TLS is handled by Traefik via Let's Encrypt. The `forward-auth` middleware (Google OAuth) is applied by default — remove that label from `docker-compose.traefik.yml` if you want the app public or use a different auth mechanism.

> **Note:** The frontend nginx is the only container on `proxy_net`. It proxies `/tasks/api/` and `/tasks/ws` to `backend:8080` over the stack's private default network, so the backend has no public port and is not reachable from other containers on `proxy_net`. Don't attach the backend to `proxy_net`: with no `API_TOKEN` set, every neighbouring container could then drive the API. (The private network is deliberately not `internal: true`, because agents need outbound access to git remotes and model APIs.)

## Install on your phone

ATE can be installed as a home-screen app. Open the site in Chrome on Android and either use **Settings → Install app** or the browser menu's *Install app* / *Add to Home screen*. The installed app opens on the Inbox, and long-pressing its icon offers Inbox, Board and Chat shortcuts.

The phone must reach the server over **HTTPS** (or `localhost`) — browsers only offer installation and allow notifications in a secure context. Plain `http://` to a LAN IP will not work; put the server behind a TLS proxy (see Traefik above). The app still loads from the network; there is no offline cache.

## Set an API Token in Production

With no `API_TOKEN`/`API_TOKENS`, the backend accepts unauthenticated requests from anything that can reach port 8080: other containers on a shared Docker network, LAN peers if `ATE_BIND_ADDR` is widened, and agent processes via `localhost:8080`. That API can register repos and start shell-executing agent runs, so set a token for any deployment that is not a single-user laptop.

- `./run.sh` generates a random `API_TOKEN` into `.env` on first start if neither variable is set (it never overwrites an existing value) and prints it. The board asks for it once on first load; paste it in. API clients send `Authorization: Bearer <token>`.
- Otherwise set it yourself: `echo "API_TOKEN=$(openssl rand -hex 32)" >> .env`, then restart. Both compose files pass `API_TOKEN` and `API_TOKENS` through from `.env`/your shell.
- The backend logs a `SECURITY:` warning at startup when no token is set and it listens on all interfaces (it always binds `:PORT` inside the container; the host-side publish is loopback-only unless you set `ATE_BIND_ADDR`). The same condition shows as an "API authentication" warning on the Health page. `/healthz` and `/readyz` are unaffected, so container healthchecks keep passing.
- To check what a running deployment uses: `docker exec <backend-container> printenv API_TOKEN` (empty output or exit status 1 means no token is set). Also check `API_TOKENS`.

## Backend Resilience: Restart Policy, Readiness, and Memory Limit

The `backend` service in `docker-compose.yml` / `docker-compose.release.yml` is configured with:

- `restart: unless-stopped` — the container is restarted automatically after a crash or OOM kill (and on daemon restart), unless you've explicitly stopped it.
- A healthcheck against `GET /readyz` (not `/healthz`) — this actually checks DB connectivity and that the dispatch loop is still ticking, so Docker detects a wedged backend (e.g. a locked SQLite file or a hung sweep) instead of it appearing healthy forever. See [`docs/api.md`](api.md#get-readyz).
- A default memory ceiling of **4 GB** via `deploy.resources.limits.memory` — headroom for the Go backend plus the agent CLIs (`claude`/`node`/etc.) it shells out to, including memory-hungry steps like a frontend test suite running under several concurrent agents. If you run many concurrent agent runs and still hit this ceiling (the container gets OOM-killed and auto-restarts), raise `memory:` under the backend service's `deploy.resources.limits` in your compose file.

## Single-replica deployment

Run exactly one backend replica. Some state is held in process memory (WebSocket tickets, the notify webhook debounce, chat terminal PTYs), so horizontal scaling is not supported. Agent rate-limit blocks are persisted and survive restarts.

## Environment Variables

All variables can also be set via a YAML config file pointed to by `CONFIG_FILE` (env vars always take precedence over file values).

### Core

| Variable | Default | Description |
|---|---|---|
| `ATE_BIND_ADDR` | `127.0.0.1` | **Not read by the Go server** — consumed by `docker-compose.yml`/`docker-compose.release.yml` to pick the host address the `backend` (`8080`) and `frontend` (`5173`) ports are published on. Defaults to loopback-only; set to `0.0.0.0` to publish on all interfaces, and only alongside `API_TOKEN`/`CORS_ORIGINS` above. No effect when using `docker-compose.traefik.yml`, which drops host port bindings entirely. |
| `PORT` | `8080` | Backend HTTP port |
| `DB_PATH` | `agent-task-editor.db` | SQLite database file path |
| `API_TOKEN` | _(empty)_ | Bearer token for API auth; empty = no auth. Requests using this token are recorded anonymously (no actor name) in the label history audit trail — see `API_TOKENS` below for named tokens. |
| `API_TOKENS` | _(empty)_ | Comma-separated named bearer tokens, format `name1:token1,name2:token2`. Any of these tokens authenticates like `API_TOKEN`, but the matching name is recorded as the actor in `task_label_history.actor_id` for human-triggered transitions (approve/reject/move label), and surfaced via `GET /tasks/{id}/label-history`. Can be combined with `API_TOKEN` (kept for backward compatibility as an anonymous fallback). If the same token string is reused across multiple names, the last one loaded wins. |
| `METRICS_TOKEN` | _(empty)_ | Dedicated bearer token for `GET /metrics`. Empty = the API token(s) are required when `API_TOKEN`/`API_TOKENS` is set, otherwise open (see [Metrics](#metrics)) |
| `METRICS_PUBLIC` | `false` | `true` leaves `GET /metrics` unauthenticated even when API auth is configured and `METRICS_TOKEN` is unset |
| `CORS_ORIGINS` | `http://localhost:5173,http://localhost:8080` | Comma-separated allowed origins; set to `*` explicitly to allow any origin (not recommended outside trusted single-user localhost use) |
| `ALLOWED_HOSTS` | _(empty)_ | Comma-separated extra `Host` header values (hostname or `host:port`) accepted **only while no `API_TOKEN`/`API_TOKENS` is set** — DNS-rebinding protection for unauthenticated instances. Always allowed: `localhost`, `127.0.0.1`, `::1`, `backend`, any IP literal, and the hostnames in `CORS_ORIGINS`. Set it if you reach an unauthenticated instance by a custom name (e.g. `ate.lan`); rejected requests get a 403 naming this variable. `*` disables the check. Ignored (check off) when a token is configured. |
| `MAX_WORKERS` | `5` | Maximum concurrent agent runs |
| `ISSUE_SYNC_INTERVAL` | `60s` | Poll interval for the GitHub Issues importer ([task-sources.md](task-sources.md)) |
| `SCHEDULE_INTERVAL` | `30s` | Poll interval for the recurring task-schedule sweep ([task-templates.md](task-templates.md#recurring-schedules)) |
| `CONFIG_FILE` | _(empty)_ | Path to a YAML config file (all keys are optional) |
| `AGENT_ISOLATION` | `true` | Run provider CLIs (task runs, chat terminals) as a separate unprivileged `agent` user. `false` runs them as the server user. See [Agent user isolation](#agent-user-isolation). |
| `AGENT_UID` | `10001` | uid of the `agent` account (set by `entrypoint.sh`; bumped to `10002` if it equals `PUID`). Its gid is always `PGID`. |
| `AGENT_FIX_REPO_PERMS` | `auto` | `auto`: at startup add group-write (`chmod -R g+rwX`) to each repo directly under `REPO_BASE_DIR` whose top-level dir isn't already group-writable. `always`: re-walk every start. `never`: skip (manage permissions yourself). |

### Cost Controls

| Variable | Default | Description |
|---|---|---|
| `MAX_DAILY_COST_USD` | `0` (unlimited) | Global cap on total recorded agent spend for the current UTC calendar day, across every task/provider/agent config. Once reached, the dispatcher stops starting new runs system-wide (in-flight runs finish normally) until the next UTC day. Distinct from the per-task `max_cost_usd` budget — see [agents.md#global-cost-ceiling](agents.md#global-cost-ceiling). |
| `MAX_MONTHLY_COST_USD` | `0` (unlimited) | Same as above, for the current UTC calendar month. |

### Notifications

Outbound webhook notifications when a task needs a human — see [websocket.md#outbound-webhook](websocket.md#outbound-webhook) for the full trigger list, JSON payload schema, debounce, and retry behavior.

| Variable | Default | Description |
|---|---|---|
| `NOTIFY_WEBHOOK_URL` | _(empty, disabled)_ | Target URL for outbound task-needs-human notifications (generic JSON POST — pair with something like a webhook relay for Slack/Discord/ntfy). Treat this as a **secret**: many webhook URLs (e.g. Slack incoming webhooks) embed an auth token in the path, and it is never logged in full by the server. |
| `NOTIFY_BASE_URL` | _(empty)_ | Base URL used to build a deep link (`<base>/tasks/<id>`) in each notification payload. **Required for a working link** — without it, the payload simply omits the `url` field rather than guessing a server-local address that wouldn't resolve for whoever receives the notification. |
| `NOTIFY_DEBOUNCE` | `5m` | Minimum time between two notifications sharing the same task+reason, so a retry storm doesn't spam the webhook. Only meaningful when `NOTIFY_WEBHOOK_URL` is set. |

### Repository Access

| Variable | Default | Description |
|---|---|---|
| `REPO_BASE_DIR` | _(empty)_ | Restrict repo registration to paths under this directory. Supports `~/` prefix. If unset, any host path can be registered (not recommended in production). |
| `UPLOAD_DIR` | `uploads` (next to DB) | Directory for task attachment uploads |

### Forge / Gitea

These are read directly via `os.Getenv` by `internal/forge/gitea`, not through the config struct — they **cannot** be set via `CONFIG_FILE` YAML.

| Variable | Default | Description |
|---|---|---|
| `GITEA_HOST` | _(empty)_ | Comma-separated git remote host(s) handled by the self-hosted Gitea forge (e.g. `git.example.com,gitea.internal:3000`). **Required to enable Gitea support at all** — with it unset the Gitea forge/issue source is never registered and every repo falls back to GitHub. See [task-sources.md](task-sources.md). |
| `GITEA_TOKEN` | _(empty)_ | Gitea personal access token with repo read/write scope, sent as `Authorization: token <value>`. Empty = anonymous (public repos only). |
| `GITEA_BASE_URL` | `https://<first GITEA_HOST>` | Override for the Gitea API base URL — set when the API is reached at a different scheme/host/port than what appears in git remote URLs. |

### AI Providers

| Variable | Default | Description |
|---|---|---|
| `MCP_SERVER_PATH` | _(empty)_ | Path to the `mcp-server` binary. Required for MCP tools (`claude`, `qwen_code`, and `codex_cli` providers). |
| `MCP_BOARD_PATH` | _(empty)_ | Path to the `mcp-board` binary. Enables the board tools (`list_repos`/`list_workflows`/`create_task`) inside Chat-tab sessions, so a chat can create tickets. Set automatically by the Docker images and `./dev.sh dev`. See [board-mcp.md](board-mcp.md). |

### Shutdown

| Variable | Default | Description |
|---|---|---|
| `SHUTDOWN_DRAIN_TIMEOUT` | `60s` | On SIGTERM/SIGINT, how long in-flight agent runs may keep going (no new runs start) before the remainder are cancelled and recorded as `interrupted`. `0` interrupts immediately. Interrupted runs are not failures: the task stays on its label, its lock is released, and it re-dispatches after restart (resuming the provider session when the agent config has `resume_sessions` on and the provider supports it, otherwise re-running normally). Runs left running by a crash or SIGKILL become `interrupted` on the next boot. Set your container's stop grace period (docker compose `stop_grace_period`) above this value or the runtime will SIGKILL mid-drain. |

### Chat Terminal Sessions

Each Chat-tab session keeps a live interactive CLI subprocess (plus a scrollback buffer) running across WebSocket disconnects so a browser refresh reattaches to the same session. By default nothing bounds how many of these accumulate or how long an unattached one stays alive; the following opt-in settings cap that growth.

| Variable | Default | Description |
|---|---|---|
| `CHAT_MAX_SESSIONS` | `0` (unlimited) | Maximum number of concurrent chat terminal subprocesses. Only refuses *new* sessions once at the cap (`ErrTooManySessions`, surfaced as the WebSocket closing with an error) — reattaching to an already-running session is never blocked by the cap. |
| `CHAT_IDLE_TIMEOUT` | `0` (disabled) | If set, how long a chat terminal session may go without an attached WebSocket connection before it's reaped (subprocess killed, scrollback released). Accepts Go duration strings (e.g. `2h`, `30m`). |

### Backup

| Variable | Default | Description |
|---|---|---|
| `BACKUP_DIR` | _(empty)_ | If set, enables the built-in scheduler that periodically writes a rotated `VACUUM INTO` snapshot of the database to this directory. Empty = disabled (on-demand backup via `GET /api/v1/backup` and the Health page's "Download backup" button is always available regardless). This is the only backup setting that requires a restart to change. |
| `BACKUP_INTERVAL` | `24h` | **Initial** value only — seeds the DB-backed setting on first migration. How often the scheduler writes a new snapshot. Accepts Go duration strings. Only meaningful when `BACKUP_DIR` is set. Editable afterwards without a restart via `PUT /api/v1/backup/settings` or the Health page (10-minute minimum). |
| `BACKUP_KEEP` | `7` | **Initial** value only — seeds the DB-backed setting on first migration. Number of most-recent snapshots to retain in `BACKUP_DIR` before pruning older ones. Editable afterwards without a restart via `PUT /api/v1/backup/settings` or the Health page. |
| `LOG_RETENTION_DAYS` | `30` | Built-in pruner deletes `agent_logs` rows for terminal-status runs (`completed`/`failed`/`waiting_human`) older than this many days. `0` = keep forever (disabled). Fresh installs start at 30; existing databases keep whatever is already stored. |
| `LOG_RETENTION_INTERVAL` | `1h` | How often the log pruner runs. Accepts Go duration strings. Only meaningful when `LOG_RETENTION_DAYS` is set. |
| `WORKTREE_SWEEP_INTERVAL` | `10m` | How often the built-in orphan-worktree sweeper reconciles each repo's `.ate-worktrees/<id>` directories against live (non-archived) task/chat-session ids, reclaiming anything else — worktrees left behind by archiving a task on a non-terminal label, or orphaned by a crash. Always on (unlike `BACKUP_DIR`, there's no disable switch); this only controls the interval. Accepts Go duration strings (1-minute minimum). |
| `GIT_TIMEOUT` | `120s` | Bounds every individual `git` shell-out made by the agent package (worktree `fetch`/`add`, safety-net `commit`, `push`, subtask `merge`, branch cleanup). Without this bound a stalled remote or an interactive credential prompt could hang a `git` call indefinitely, blocking the whole dispatch sweep loop (tasks dispatch serially) since nothing else could run. With it, a stuck git call fails just that one task within `GIT_TIMEOUT`, is classified as a transient failure eligible for retry, and dispatch keeps working on the rest of the board. Interactive credential prompts are also suppressed (`GIT_TERMINAL_PROMPT=0` and related env vars) so an auth prompt fails fast rather than hanging on stdin. Accepts Go duration strings. |

See [backup.md](backup.md) for the full backup/restore guide, including the "Agent log retention" and "Orphaned worktree sweeper" sections.

### Other

| Variable | Default | Description |
|---|---|---|
| `GITHUB_SYNC_INTERVAL` | `30s` | How often to poll GitHub for PR status updates. Accepts Go duration strings (e.g. `1m`, `5m`). |
| `LOG_LEVEL` | `INFO` | Logging level: `DEBUG`, `INFO`, `WARN`, `ERROR` |
| `AGENT_RAW_LOG_DIR` | _(empty)_ | Dev/debug only. If set, every raw stream-json line from CLI providers (`claude`, `codex`, `qwen_code`, `opencode`) is written verbatim to `<dir>/<run_id>.jsonl` before parsing — useful for reviewing what the CLIs emit vs. what the app extracts. No retention or compression; clean up the directory yourself. Off by default. With `dev.sh`, pass `--raw-log-dir <path>` instead of setting this directly: `dev` mode writes to that host path, while docker `start`/`restart` redirect to `/data/raw-logs` on the `db_data` volume (get files out via `./dev.sh shell`). |
| `UPDATE_CHECK_ENABLED` | `false` | If `true`, the Health page's `update_check` row shells out to `gh release view` to compare the running version (`GET /healthz`) against the latest GitHub release and warns when one is available. Disabled by default so the app never phones home without opting in; degrades to a warning (never an error) when offline or `gh` isn't configured. See [api.md](api.md#get-healthproviders). |

### YAML Config File

If `CONFIG_FILE` points to a YAML file, values from it are used as defaults (env vars override):

```yaml
port: "8080"
db_path: agent-task-editor.db
api_token: ""
api_tokens:
  alice: tok-alice
  bob: tok-bob
metrics_token: ""
metrics_public: false
cors_origins: "*"
mcp_server_path: /path/to/mcp-server
max_workers: 5
repo_base_dir: /repos
upload_dir: /data/uploads
github_sync_interval: 30s
backup_dir: /data/backups
backup_interval: 24h
backup_keep: 7
```

### Authentication

Set `API_TOKEN` to require an `Authorization: Bearer <token>` header on all API requests. Since browsers cannot set custom headers on WebSocket upgrades, WS connections instead first `POST /api/v1/ws-ticket` (Bearer-authed) to mint a short-lived, single-use ticket, then connect with `?ticket=<value>` — see [websocket.md](websocket.md). The frontend does this using a runtime token (see below), not a build-time one. The former `?token=<value>` query-string fallback has been removed (it leaked the token into proxy logs and browser history); non-browser clients must use the ticket flow too.

To identify *who* performed a human-triggered transition (approve/reject/move label) in the audit trail, set `API_TOKENS` (format `name1:token1,name2:token2`, or the `api_tokens` map in the YAML config) instead of, or alongside, `API_TOKEN`. Each named token authenticates the same way, but the resolved name is recorded as `actor_id` in `task_label_history` and returned by `GET /tasks/{id}/label-history`. `API_TOKEN` remains supported as a legacy/anonymous fallback — requests using it are recorded with an empty actor, exactly as before this feature existed. Named tokens work with `POST /api/v1/ws-ticket` like any other API route.

**Frontend token entry.** Once `API_TOKEN` is set on the backend, the first API request the UI makes will come back `401`, and the UI shows a simple one-field "enter API token" screen instead of a broken board. Enter the token once; it's stored in the browser's `localStorage` and sent as `Authorization: Bearer <token>` on every subsequent request and WS ticket mint, for every page (board, task detail incl. the live log stream, workflows, agents, repos, health, backup download). A wrong or expired token sends you back to that screen rather than leaving the UI in a broken state. With `API_TOKEN` unset, no prompt ever appears. This is a runtime flow — it works with the prebuilt/GHCR image exactly the same as a locally built one, since nothing needs to be baked in at build time.

The frontend reads `VITE_API_BASE_URL` and `VITE_WS_BASE_URL` at build time. For the Docker image these default to `""` (same origin). For local development add a `.env.local` in `frontend/`:

```
VITE_API_BASE_URL=http://localhost:8080
VITE_WS_BASE_URL=ws://localhost:8080
VITE_API_TOKEN=your-token-here
```

`VITE_API_TOKEN` is a **dev-only convenience**: if set, it seeds the runtime `localStorage` token the first time the app loads (so you don't have to click through the token prompt during `npm run dev`). It's not required, and it cannot be baked into the prebuilt GHCR image — that's exactly why the runtime prompt above exists.

## Metrics

`GET /metrics` exposes Prometheus text-exposition-format metrics (dispatcher/pool
state, run/cost/token counters, WebSocket hub stats, GitHub sync sweep durations
and `gh` call counts, plus standard Go runtime metrics) — point a Prometheus
scrape config at it. It's served at the server root (not under `/api/v1`) and is
gated like the API: with `API_TOKEN`/`API_TOKENS` configured it requires one of
those tokens (`Authorization: Bearer ...`), unless you set a dedicated
`METRICS_TOKEN` for Prometheus or opt out with `METRICS_PUBLIC=true`. With no
auth configured it is open. See [api.md](api.md#get-metrics) for the full metric list.

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

`dev.sh`/`run.sh` also bind-mount `~/.claude.json` into the container. Docker creates a
*directory* at a bind-mount source path that doesn't already exist on the host, which would
break both `./dev.sh login` and host-side `claude` for first-time users — both scripts
pre-create `~/.claude.json` as an empty file (if missing) before invoking Docker Compose, so
this doesn't happen.

### Removed providers (`anthropic`, `llm`)

The direct-API `anthropic` and `llm` providers were removed in v0.17.0. `LLM_API_KEY`/`LLM_BASE_URL` are no longer read. Configs
still on them fail at dispatch until switched to `claude`, `codex_cli`, `opencode` or `qwen_code` — see
[providers/anthropic.md](providers/anthropic.md).

### Opencode (`opencode` provider)

Install the `opencode` binary (`npm i -g opencode-ai`, or build the backend image with `INSTALL_OPENCODE_CLI=true`) and configure it. **MCP tools are not available.** See [providers/opencode.md](providers/opencode.md).

### Qwen Code (`qwen_code` provider)

Install the `qwen` binary (`npm i -g @qwen-code/qwen-code`, or build the backend image with `INSTALL_QWEN_CLI=true`). MCP tools are supported (same setup as `claude`). See [providers/qwen_code.md](providers/qwen_code.md).

### Codex CLI (`codex_cli` provider)

Install the `codex` binary (`npm i -g @openai/codex`, or build the backend image with `INSTALL_CODEX_CLI=true`) and authenticate (`codex login` with ChatGPT, or `OPENAI_API_KEY`). MCP tools are supported, passed to Codex as per-run `-c mcp_servers.*` overrides. See [providers/codex_cli.md](providers/codex_cli.md).

## Mounting Repositories

Agents run with their working directory set to the registered repo path. Mount the directories you want agents to access:

```yaml
volumes:
  - /path/to/your/projects:/repos:rw
```

Then register repos using paths under `/repos` in the UI (or via the API), and set `REPO_BASE_DIR=/repos` to prevent agents from escaping that subtree.

## Agent user isolation

The server and the agents it launches used to run as the same OS user, so a
prompt-injected agent could read `/data` (the SQLite DB), the server's
environment through `/proc/<pid>/environ` (`API_TOKEN`, `GITHUB_TOKEN`, provider
keys) and `~/.config/gh`. Provider CLIs (headless runs, chat terminals, the
deprecated providers' `run_bash`/search tools) now run as a separate
unprivileged `agent` user. The server, `git`/`gh` calls it makes for push, PR
creation and PR sync, and the `mise`/`uv` toolchain installs stay on the server
user (`node`).

How it works:

- `entrypoint.sh` creates/remaps `agent` (uid `AGENT_UID`, default `10001`;
  primary group = node's group, i.e. `PGID`) and starts the server as `node`
  with `CAP_SETUID`, `CAP_SETGID` and `CAP_KILL` as ambient capabilities
  (`setpriv`). The server uses them to start each agent process with the agent
  uid/gid and to cancel it later.
- Those capabilities are **not** given to agents: every agent command is wrapped
  in `setpriv --inh-caps=-all --ambient-caps=-all`, so the CLI starts with empty
  capability sets and cannot switch back to `node`. At startup the server
  verifies this with a live probe (uid correct, all capability sets empty) and
  disables isolation with an error if the check fails.
- The server's own children (`git`, `gh`, `mise`, `uv`, ...) are wrapped the same way via `agentuser.ServerCommand`, so a hook or config an agent plants in a repo (`core.fsmonitor`, `core.hooksPath`, ...) never runs with those capabilities; only the server process itself holds them (a source-scanning test rejects raw `exec.Command` elsewhere).
- `/data` is `0700 node`; the server's `~/.config/gh` is not readable by `agent`;
  the agent has its own `HOME=/home/agent` (own `.gitconfig`).
- Shared by group (`PGID`, with `umask 002`): bind-mounted repos and
  `.ate-worktrees/`, the `mise`/`uv`/Go caches, the qwen home, per-run temp dirs
  (MCP config/result). At startup the entrypoint adds the group
  write bit to repos under `REPO_BASE_DIR` (`AGENT_FIX_REPO_PERMS`) and to
  `~/.claude` / `~/.claude.json`, which stay mounted read-write and fully usable
  by the agent (Claude writes sessions and refreshes tokens there). The compose
  files mount them a second time under `/home/agent`; older compose files still
  work through symlinks.
- Fallback, never a startup failure: if the server cannot switch users
  (`AGENT_ISOLATION=false`, running outside Docker such as `./dev.sh dev`, a
  compose `user:` override, dropped capabilities) agents run as the server user
  and the log says why (`agent user isolation is OFF`).

Caveats: files agents create are owned by `agent:<PGID>` (group-writable, so you
and the server can edit them). Tools that create directories with a hard-coded
`0755` (ignoring the umask) leave them non-writable by the server/host user;
worktree cleanup of such a tree may need `chmod -R g+w`. If your repos live in a
group other than `PGID`, set `AGENT_FIX_REPO_PERMS=never` and grant the agent
access yourself. `~/.claude` is still writable by agents by design, so Claude
credentials are not protected from them.

## Supported Languages & Extending the Toolchain

Agent `Bash`/`run_bash` tool calls execute *inside the backend container*, against your bind-mounted repos (see [Mounting Repositories](#mounting-repositories) above). That means any compiler, runtime, or build tool an agent needs to build, lint, or test a repo must be installed in `backend/Dockerfile`'s final runtime image — not just available on your host machine.

> **Note:** `frontend/Dockerfile` only builds and serves Agent Task Editor's own UI in production. It is not involved in running agent commands against your repos, so it doesn't need any of this.

### Currently supported out of the box

The backend image (`backend/Dockerfile`) ships with:

- **Go 1.27** — the same toolchain version used to build this project's own `server`/`mcp-server` binaries, copied from the Dockerfile's `golang:1.27-bookworm` builder stage into the final image (rather than installed via `apt`) so the agent-visible `go version` always matches exactly. `GOPATH`/`GOCACHE`/`GOMODCACHE` are set to writable locations under `/home/node` for the non-root `node` user.
- **Node.js 26 / npm** — inherited from the `node:26-bookworm-slim` base image. Covers Vite, React, TypeScript projects and their usual workflows out of the box: `npm ci`, `npm run build`, `npm test`, `npx vitest`, etc.
- **`build-essential`** (gcc, g++, make, libc-dev) — needed for `cgo` builds (this repo's own backend depends on `mattn/go-sqlite3`, which is cgo) and for any npm packages with native addons that need `node-gyp` compilation.
- **`git`, `bash`, `gh`** — for cloning, diffing, committing, and interacting with GitHub from inside agent runs.
- **`mise` and `uv`** — power the per-repo language version pinning feature (see [runtime.md](runtime.md)): a repo can pin `go`/`node`/`python`/`rust`/`ruby`/`java`/`godot` versions independent of what's baked into the image, installed on demand before each agent run.

### Per-repo toolchain versions vs. editing the image

Before editing the Dockerfile, check whether [runtime.md](runtime.md) already covers your need: if a repo just needs a *different version* of `go`, `node`, `python`, `rust`, `ruby`, `java`, or `godot` than the image's baseline, pin it on that repo's Repos page config instead of rebuilding the image — `mise`/`uv` install it on demand per task, with no image rebuild and no effect on other repos.

Editing the Dockerfile is for adding a *new* language/tool to the image baseline (one not in the mise-pinnable list above, or a tool every repo should have without per-repo configuration).

### Adding more languages/compilers to the image

To give agents the ability to build/test repos in another language, edit the **final stage** of `backend/Dockerfile` (the `FROM node:26-bookworm-slim` stage — don't touch the builder stage, which only compiles this project's own Go binaries) and add one of:

- **Debian packages**, e.g.:
  ```dockerfile
  RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-pip && rm -rf /var/lib/apt/lists/*   # Python
  RUN apt-get update && apt-get install -y --no-install-recommends openjdk-17-jdk && rm -rf /var/lib/apt/lists/*        # Java
  RUN apt-get update && apt-get install -y --no-install-recommends ruby ruby-dev && rm -rf /var/lib/apt/lists/*         # Ruby
  ```
- **Multi-stage `COPY --from=`**, the same technique used for Go above — pull a toolchain from an upstream image without bloating the final image with its own build dependencies:
  ```dockerfile
  FROM rust:1-bookworm AS rust-builder
  ...
  FROM node:26-bookworm-slim
  COPY --from=rust-builder /usr/local/cargo /usr/local/cargo
  COPY --from=rust-builder /usr/local/rustup /usr/local/rustup
  ENV PATH="/usr/local/cargo/bin:${PATH}"
  ```

After editing the Dockerfile, rebuild with:

```bash
docker compose build backend
# or
./dev.sh restart
```

**A note on the switch from Alpine:** the image used to be Alpine-based (`node:26-alpine`, `musl libc`), which meant some prebuilt binaries (certain Rust crates, precompiled CLI tools, some Python wheels expecting `glibc`) needed workarounds or wouldn't run at all. The image moved to `node:26-bookworm-slim` (Debian, `glibc`) specifically so `mise`'s prebuilt python/ruby/java toolchains work — see [runtime.md](runtime.md). That also removes the Alpine/glibc caveat for anything you add to the image yourself: Debian packages and most prebuilt Linux binaries work without the `gcompat` workaround Alpine used to need.

Remember to make sure any new tool's cache/config directories are writable by the `node` user and, because agents run as the `agent` user in `node`'s group, group-writable (see [Agent user isolation](#agent-user-isolation); add them to the `group_rw` list in `entrypoint.sh`). The container runs as `node`, which `backend/entrypoint.sh` remaps to your host `PUID`/`PGID` at startup and `chown`s the state dirs (`/data`, `/app`, the Go caches, `mise`/`uv`'s caches under `/home/node`) accordingly. If your tool writes elsewhere under `/home/node`, add it to that `chown` list — otherwise agents will hit permission errors the first time they invoke the tool.

## Local Development

### Backend

```bash
cd backend
go run ./cmd/server
```

Requires Go 1.25. The database file is created automatically with migrations applied on startup.

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

Run the unit/component test suite with `npm run test:coverage` (Vitest +
Testing Library). A small Playwright E2E smoke suite also exists
(`frontend/e2e/`), covering board load, task creation, task-detail
navigation, and the Logs tab's WS log pane mount, plus a check that every
static app route (dashboard, board, chat, workflow, and all configuration
pages) loads correctly — each spec runs on both a desktop and a mobile
viewport (Playwright's `chromium` and `mobile-chrome` projects). It runs
against the built docker-compose stack rather than `npm run dev`, so start
the stack first (`./dev.sh start` or `docker compose up -d --build --wait`),
then run `npm run e2e` from `frontend/` (see `frontend/e2e/README.md` for
the one-time demo-repo setup it needs).

### Building the MCP Sidecar

The MCP sidecar enables the `signal_complete`, `request_human`, `update_task_notes`, and `store_info` tools for the `claude` and `qwen_code` providers:

```bash
cd backend
go build -o mcp-server ./cmd/mcp-server
```

Set `MCP_SERVER_PATH=/path/to/mcp-server` in the backend environment. `./dev.sh dev` does this automatically.

## First Steps After Startup

1. **Register a repository** — go to Repos → Add Repo. Enter the local filesystem path of the repository agents should work in.
2. **Staff the default workflow** — the default workflow needs an agent on every agent-driven stage (`plan`, `work`, `testing`, `agent-review`), so a single agent config isn't enough: the next task would stall with "No agent config". The Board's Get started checklist has a one-click **Create Planner, Worker, Tester, Reviewer** button that builds the missing ones from the built-in templates against the provider config you pick (it is disabled until a provider exists). To do it by hand, go to Pipeline → Agents → New Agent. Select a provider, enter a model name, set target labels (e.g. `["plan", "work"]`), and optionally write a system prompt.
3. **Create a task** — go to the Board, click New Task. Select the repo and fill in the title/description. To create a variant of an existing task instead, use the "⧉ Duplicate" action (task detail header or a board card's hover controls) to open the same form pre-filled from that task.
4. **Move it to `plan`** — drag it or use the label selector. The dispatcher will pick it up within 5 seconds and start an agent run.
5. **Watch the logs** — click on the task to open the detail view; live logs stream in real time.
6. **Review and open a PR** — in the default workflow, the `review` label is marked `create_pr`: when a task reaches `review`, the backend pushes its `ate-…` branch and opens (or reuses) a pull request. This needs a repo with a GitHub or Gitea remote (`remote_url`) and working push/`gh` auth; on a local-only repo it is skipped with a log line and the task is unaffected. Approve then moves the task to `done`. To open the PR yourself (or retry after a failure), use the **Create PR** button in the task detail header or the Diff tab. Workflows that already exist keep their current settings; fresh installs only get this default. See `docs/workflows.md`.

> **In-app guidance:** until the instance is fully set up, the Board shows a dismissible onboarding checklist that walks through these steps in order (add a repo → configure a provider → staff the default workflow → create your first task), checking each one off automatically as it's completed. It also surfaces any failing `/health/providers` readiness check (e.g. Claude CLI not authenticated) inline against the step it blocks, so problems show up where you're already looking instead of only on the Health page. Dismiss it any time — it stays dismissed, and it stops appearing on its own once every step passes. Until setup is complete (and unless you dismiss the checklist), opening the app at `/` takes you to the Board instead of the Overview; the Overview is always at `/dashboard`. Failing readiness checks stay visible after the checklist is gone: a banner across the top of every page lists them, and the Settings nav item (plus the Health tab inside Settings) shows a red/amber dot until they pass.
