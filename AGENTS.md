# Agent Task Editor

A self-hosted Kanban board where AI agents automatically process tasks as they move through workflow columns. A label-based state machine governs task progression; the dispatcher picks up tasks in agent-triggerable states and runs the configured AI provider against the associated repository.

## Repository Layout

```
agent-task-editor/
├── docker-compose.yml        # Build-from-source deployment (backend :8080, frontend :5173)
├── docker-compose.release.yml # Deploy from prebuilt GHCR images (see run.sh)
├── run.sh                    # Start the stack from published images (prod counterpart to dev.sh)
├── CHANGELOG.md              # Release history; drives GitHub Release notes
├── openapi.yaml              # Full REST API spec
├── docs/                     # Feature and usage documentation
│   ├── adr/                  # Architecture Decision Records (rationale and history; AGENTS.md files state only current rules)
│   ├── README.md
│   ├── overview.md
│   ├── getting-started.md
│   ├── workflows.md
│   ├── agents.md
│   ├── providers/            # Per-provider deep-dives (claude, codex_cli, opencode, qwen_code; anthropic/llm removed in v0.17.0)
│   ├── api.md
│   ├── task-sources.md
│   ├── task-templates.md
│   ├── lessons.md
│   ├── websocket.md
│   ├── mcp-tools.md
│   ├── board-mcp.md
│   ├── runtime.md            # Per-repo agent toolchains via mise/uv
│   ├── backup.md
│   └── screenshots.md
├── backend/                  # Go server (go.mod: 1.26; CI + Docker builder: 1.27.3)
│   ├── cmd/server/           # Main entrypoint
│   ├── cmd/mcp-server/       # MCP sidecar (signal_complete / request_human)
│   ├── cmd/mcp-board/        # Standalone MCP server for creating board tickets from a chat client (see docs/board-mcp.md)
│   └── internal/
│       ├── agent/            # Provider system, pool, dispatcher
│       ├── agentuser/        # Runs agent subprocesses as the unprivileged `agent` OS user (Credential switch + capability stripping); leaf package
│       ├── api/              # Chi router, handlers, middleware
│       ├── boardtools/       # Board MCP tools (list/create/approve/reply…) as REST wrappers; shared by cmd/mcp-board and remotemcp
│       ├── backup/           # Optional rotated local `VACUUM INTO` snapshot scheduler (see docs/backup.md)
│       ├── config/           # YAML + env var config
│       ├── cronexpr/         # Minimal dependency-free 5-field cron evaluator used by task schedules
│       ├── forge/            # Forge interface + forge-neutral types (git-hosting-provider seam); leaf package (no ghclient/ghsync/tasksource/writeback/metrics imports); two impls today
│       │   └── gitea/        # Gitea `forge.Forge` impl: direct REST calls (no CLI dependency)
│       ├── gitcmd/           # Single funnel for the server's `git` subprocesses (via agentuser; Output/Run/Combined, no-prompt env) + shared ref validator `IsValidRef`; leaf (agentuser + stdlib only)
│       ├── ghclient/         # GitHub `forge.Forge` impl: direct GitHub REST client (token from GH_TOKEN/GITHUB_TOKEN/`gh auth token`) (PR lookup/create, issue listing, repo name parsing)
│       ├── ghsync/           # Background poller: refreshes task PR state (incl. merge conflicts) via the repo's `forge.Forge`
│       ├── health/           # Provider readiness checks (Claude CLI, MCP sidecar, GitHub auth, repo base dir)
│       ├── intake/           # Intake routing rules: match->apply engine shared by tasksource + schedule + intakehook, plus the safe {{path}} template renderer; leaf package, no deps beyond storage/gen
│       ├── intakehook/       # Signed inbound webhook events -> tasks (Shape/Receive, dedupe into open tasks)
│       ├── logretention/     # Periodic pruning of agent_logs for terminal runs; DB-backed settings
│       ├── metrics/          # Process-wide Prometheus registry and custom collectors (leaf package)
│       ├── memguard/         # Last-resort memory watchdog that cancels a single runaway agent run
│       ├── needsme/          # Single server-side definition of which tasks need a human and why (drives GET /needs-me, Needs-you panel/review popup, badges)
│       ├── notify/           # Optional outbound webhook when a task needs a human (NOTIFY_WEBHOOK_URL)
│       ├── autonomy/         # Pure per-gate autonomy evaluator (leaf, stdlib only): modes, config validation, Evaluate
│       │   └── policy/       # Async event subscriber that records autonomy decisions on gate landing and acts on approvals (hold-window sweeper, approves as actor "policy")
│       ├── reviewbrief/      # Deterministic review brief for human-gate tasks: diff risk flags, verification/run summary (GET /tasks/{id}/review-brief, needs-me risk_flags); also owns the shared task-diff git logic
│       ├── remotemcp/        # Remote MCP endpoint (/mcp, streamable HTTP) + Google-backed OAuth server for claude.ai connectors; off unless MCP_REMOTE_ENABLED=true
│       ├── prflow/           # Push a task's branch and create/find its PR; shared by the CreatePR handler and the OnCreatePR hook (leaf: no api/handlers or workflow import)
│       ├── scopedtoken/      # Short-lived, route-scoped bearer tokens minted for the MCP sidecars (instead of the admin API token)
│       ├── schedule/         # Fires task_schedules on their cron interval, creating tasks from templates
│       ├── storage/          # SQLite, golang-migrate, sqlc-generated code
│       ├── tasksource/       # Task import + ongoing sync from external trackers (GitHub Issues today, via `forge.Forge`)
│       ├── workflow/         # State machine engine
│       ├── worktreesweep/    # Periodic reconciliation/GC of each repo's .ate-worktrees/<id> dirs
│       ├── writeback/        # Per-repo opt-in status write-back to the source issue (label / PR comment / close)
│       ├── ws/               # WebSocket hub and client management
│       └── wsevent/          # Typed payloads for every /ws event, checked against openapi.yaml by a test
└── frontend/                 # React + TypeScript + Vite + Tailwind
    └── src/
        ├── api/              # REST client, WS client, shared types
        ├── components/       # Board, diff viewer, shared UI
        ├── pages/            # Board, Home (components/home/ReviewPopup), TaskDetail, Pipeline (overview/workflow/agents), Settings (providers/pricing/intake/templates/health), Repos, Chat, Insights (Dashboard/Usage/Performance)
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
| `API_TOKENS` | _(none)_ | Named bearer tokens (`name1:token1,name2:token2`); resolves to an actor name recorded in `task_label_history.actor_id` for human-triggered transitions, surfaced via `GET /tasks/{id}/label-history` |
| `ALLOWED_HOSTS` | _(none)_ | Extra `Host` header values (hostname or `host:port`, comma-separated) accepted while no `API_TOKEN`/`API_TOKENS` is set (DNS-rebinding protection). Always allowed: `localhost`, `127.0.0.1`, `::1`, `backend`, IP literals and hostnames from `CORS_ORIGINS`. `*` disables the check; ignored when a token is set |
| `REPO_BASE_DIR` | _(none)_ | Restrict repo paths; empty = any path allowed (warns on startup) |
| `AGENT_ISOLATION` | `true` | Run provider CLIs as a separate unprivileged `agent` OS user (server keeps `CAP_SETUID/SETGID/KILL`, children get none; falls back to the server user with a warning when it can't switch). `false` opts out. See `docs/getting-started.md#agent-user-isolation` |
| `AGENT_UID` | `10001` | uid of the `agent` account (gid = `PGID`); set up by `entrypoint.sh`, read by `internal/agentuser` |
| `AGENT_FIX_REPO_PERMS` | `auto` | `auto`/`always`/`never`: whether the entrypoint adds group-write to repos under `REPO_BASE_DIR` for the agent user |
| `SKIP_PREMIGRATE_SNAPSHOT` | `false` | Set `true` to skip the automatic pre-migration `VACUUM INTO` snapshot (written next to the DB file whenever migrations are pending; last 3 kept) and to start anyway if it cannot be written. See `docs/backup.md#pre-migration-snapshots`. |
| `GITHUB_WEBHOOK_SECRET` | _(none)_ | Enables `POST /api/v1/github/webhook` (outside bearer auth, HMAC-SHA256 `X-Hub-Signature-256`); GitHub webhooks then refresh task PR state immediately and polling backs off to a fallback. Unset = route returns 404. See `docs/task-sources.md#github-webhook-optional` |
| `METRICS_TOKEN` | _(none)_ | Dedicated bearer token for `GET /metrics`. Unset + API auth configured = API token(s) required; unset + no auth = open |
| `METRICS_PUBLIC` | `false` | `true` leaves `/metrics` unauthenticated even when API auth is configured (opt-out for tokenless scrapers) |
| `MCP_SERVER_PATH` | _(none)_ | Path to mcp-server binary; enables signal_complete/request_human tools |
| `MCP_BOARD_PATH` | _(none)_ | Path to mcp-board binary; enables the board tools (list_repos/list_workflows/create_task) inside chat sessions. See `docs/board-mcp.md` |
| `MCP_BOARD_TOOL_PROFILE` | `full` | Tool set the chat-session mcp-board exposes (passed as its `TOOL_PROFILE`): `create`, `readonly`, `full` (default when unset) or `admin`. Standalone mcp-board defaults to `create`. See `docs/board-mcp.md` |
| `MCP_REMOTE_ENABLED` | `false` | Explicit opt-in for the remote MCP endpoint (claude.ai custom connectors). Other `MCP_*` vars alone never enable it; `true` with a required var missing fails startup. `./run.sh`/`./dev.sh --with-remote-mcp` set it and add `docker-compose.mcp-remote.yml` (the public Traefik route) |
| `MCP_PUBLIC_URL` | _(none)_ | Public base URL of the remote MCP endpoint (`<url>/mcp`); used only when `MCP_REMOTE_ENABLED=true`, which also needs `MCP_GOOGLE_CLIENT_ID`, `MCP_GOOGLE_CLIENT_SECRET`, `MCP_ALLOWED_EMAILS`, `MCP_SIGNING_KEY`. See `docs/board-mcp.md#remote-connector` |
| `MCP_SIGNING_KEY` | _(none)_ | Required (>= 32 chars) when `MCP_REMOTE_ENABLED=true`: signs the remote MCP's tokens and client ids, independent of the Google secret. Generate with `openssl rand -base64 48`; rotating it revokes every token |
| `MCP_TOOL_PROFILE` | `readonly` | Remote MCP tool set: `readonly` (list/get/read tools incl. list_needs_me, get_costs, list_provider_configs, get_agent, list_templates, list_schedules), `full` (adds create_task, reply_to_run, approve_task, reject_task, move_task, archive_task, cancel_run, rerun_task, create_pr) or `admin` (full plus create_workflow, update_workflow, add_repo, update_repo, create_agent, update_agent, create_template, update_template, create_schedule, update_schedule). `./run.sh`/`./dev.sh --mcp-profile <readonly\|full\|admin>` sets it per invocation (overriding `.env`); an unknown value exits 1, and the flag warns and is ignored without `--with-remote-mcp` |
| `MCP_REDIRECT_URIS` | claude.ai + claude.com connector callbacks | Exact OAuth redirect URIs the remote MCP will register and redirect to (comma-separated); loopback only if listed |
| `MCP_ALLOWED_CLIENT_IDS` | _(any)_ | Optional comma-separated OAuth client ids the remote MCP accepts; the id is logged when a client registers |
| `MAX_WORKERS` | `5` | Concurrent agent runs |
| `ISSUE_SYNC_INTERVAL` | `60s` | Poll interval for the GitHub Issues importer (see `docs/task-sources.md`) |
| `INSECURE_SKIP_SSL_VERIFY` | `false` | Set to `true` behind corporate TLS-inspecting proxies. Disables SSL verification for git clone, npm, and the claude CLI (Node.js). Set in your shell or a `.env` file — docker compose passes it as a build arg (npm install of claude-code) and runtime env vars (`GIT_SSL_NO_VERIFY`, `NPM_CONFIG_STRICT_SSL`, `NODE_TLS_REJECT_UNAUTHORIZED`). |
| `SHUTDOWN_DRAIN_TIMEOUT` | `60s` | On SIGTERM, how long in-flight runs may finish (dispatch stopped) before the rest are cancelled and marked `interrupted` (not `failed`); `0` = no drain. Keep the container's `stop_grace_period` above it. See `docs/getting-started.md` |
| `SSL_CA_CERT_PATH` | _(none)_ | Alternative to `INSECURE_SKIP_SSL_VERIFY`: path to a corporate CA `.pem` file to trust, instead of disabling verification entirely. Bind-mounted into the container and wired into git/npm/Node via `GIT_SSL_CAINFO`/`NODE_EXTRA_CA_CERTS`/`SSL_CERT_FILE`. Applies to task runs and chat sessions alike, since both spawn their provider CLI inheriting the backend's environment. |

## Development Workflow

- **Backend code gen:** `cd backend && go generate ./...` (runs sqlc after editing `.sql` files)
- **Tests:** `cd backend && go test ./...`
- **Frontend checks:** a fresh worktree has no `frontend/node_modules`. Run `cd frontend && npm ci` once before `tsc`, lint or `vitest`; don't report them as "couldn't run".
- **Frontend types:** `frontend/src/api/types.ts` is generated from the root `openapi.yaml`. When changing the API, edit `openapi.yaml`, then run `cd frontend && npm run gen:api` to regenerate the types and commit the result. Don't hand-edit `types.ts` — CI's "Check generated API types are up to date" step fails on any drift between the spec and the committed types.
- **Migrations:** add numbered files to `backend/internal/storage/migrations/`; they run automatically on startup
- **Changelog:** always add an entry under `## [Unreleased]` in `CHANGELOG.md` for any user-facing change (features, fixes, security, breaking changes). This file drives the GitHub Release notes, so keep it current with every PR.
- **Commit messages:** keep them to the plain message only (subject + body). Do **not** append a `Co-Authored-By:` trailer or a `Claude-Session:` line to any commit or merge message.

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

# Free-text search + filters (q, label, repo_id, type, git_state; combinable)
curl "http://localhost:8080/api/v1/tasks?q=flaky&type=bug"

# Include archived tasks (excluded by default; archived=only for just archived)
curl "http://localhost:8080/api/v1/tasks?archived=all"

# Archive / unarchive a task
curl -X PATCH http://localhost:8080/api/v1/tasks/<id>/archive \
  -H "Content-Type: application/json" \
  -d '{"archived": true}'

# Bulk action (move | pause | resume | archive | unarchive)
curl -X POST http://localhost:8080/api/v1/tasks/bulk \
  -H "Content-Type: application/json" \
  -d '{"ids": ["<id1>", "<id2>"], "action": "archive"}'

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

## Architecture Decisions

Rationale and history live in [`docs/adr/`](docs/adr/README.md); this file and the nested `AGENTS.md` files state only the current rules. The ones to know before touching core code:

- Two run-id columns: `active_agent_run_id` is the dispatch lock, `current_agent_run_id` is the last run (WS replay). Do not conflate them. ([ADR 0001](docs/adr/0001-active-vs-current-agent-run-id.md))
- `waiting_human` runs keep the lock; any label transition clears it via SQL. New escalation paths must keep the lock and publish `task.needs_human`. ([ADR 0002](docs/adr/0002-waiting-human-keeps-dispatch-lock.md))
- Claim and release the lock only with the compare-and-swap / owner-scoped queries, never unconditionally. ([ADR 0003](docs/adr/0003-dispatch-lock-compare-and-swap.md))
- The MCP sidecar returns results through the `RESULT_FILE` temp file, read by the runner after the subprocess exits (only `create_subtask` POSTs live). ([ADR 0004](docs/adr/0004-mcp-sidecar-result-file-handoff.md))
- `REPO_BASE_DIR` and API auth are optional (startup warnings only). Keep new safety config permissive-by-default with a loud warning. ([ADR 0005](docs/adr/0005-repo-base-dir-and-auth-are-warn-only.md))
- Agent `Bash` calls run inside the **backend** container. A compiler/runtime an agent needs must be in the *final* stage of `backend/Dockerfile` (baseline: Go 1.27, Node 26/npm, build-essential, `mise`/`uv`) or pinned per repo via the runtime feature (`docs/runtime.md`). `frontend/Dockerfile` is unrelated. To extend the baseline see `docs/getting-started.md#supported-languages--extending-the-toolchain`. ([ADR 0006](docs/adr/0006-agent-toolchain-and-runtime-pins.md))
- `frontend/src/lib/providerCapabilities.ts` is the single source of truth for per-provider capability gaps. After editing it, run `npm run gen:capability-docs` (from `frontend/`) to regenerate the table between the `<!-- BEGIN/END capability-matrix (generated) -->` markers in `docs/agents.md`. Don't hand-edit that table; CI re-runs the generator and fails on any diff. ([ADR 0007](docs/adr/0007-provider-capabilities-single-source-of-truth.md))
