# Getting Started

## Security Notice

> **Agent Task Editor executes arbitrary shell commands.** AI agents run with full shell access as the server user. The defaults are permissive and intended for localhost use only.
>
> For any non-localhost deployment, set `API_TOKEN`, `REPO_BASE_DIR`, and a tight `CORS_ORIGINS` before starting the stack. Run behind a reverse proxy or VPN. See the [Security section in the README](../README.md#security) for the full checklist.

## Prerequisites

- Docker and Docker Compose (recommended)
- **Or** Go 1.24+ and Node 20+ for local development
- An AI provider credential — see [Providers](providers/) for options

## Quick Start with Docker Compose

```bash
git clone https://github.com/myinisjap/agent-task-editor
cd agent-task-editor
./dev.sh start
```

Open `http://localhost:5173` in your browser.

The backend starts on `:8080`, the frontend nginx on `:5173`.

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

A `docker-compose.traefik.yml` override is included for running the app behind a Traefik reverse proxy. It removes the host port bindings and adds the Traefik labels needed to route traffic.

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

> **Note:** The frontend nginx is the only container exposed to Traefik. It proxies `/tasks/api/` and `/tasks/ws` to the backend internally, so the backend container has no public port.

## Environment Variables

All variables can also be set via a YAML config file pointed to by `CONFIG_FILE` (env vars always take precedence over file values).

### Core

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Backend HTTP port |
| `DB_PATH` | `agent-task-editor.db` | SQLite database file path |
| `API_TOKEN` | _(empty)_ | Bearer token for API auth; empty = no auth |
| `CORS_ORIGINS` | `*` | Comma-separated allowed origins (e.g. `http://localhost:5173`) |
| `MAX_WORKERS` | `5` | Maximum concurrent agent runs |
| `ISSUE_SYNC_INTERVAL` | `60s` | Poll interval for the GitHub Issues importer ([task-sources.md](task-sources.md)) |
| `CONFIG_FILE` | _(empty)_ | Path to a YAML config file (all keys are optional) |

### Repository Access

| Variable | Default | Description |
|---|---|---|
| `REPO_BASE_DIR` | _(empty)_ | Restrict repo registration to paths under this directory. Supports `~/` prefix. If unset, any host path can be registered (not recommended in production). |
| `UPLOAD_DIR` | `uploads` (next to DB) | Directory for task attachment uploads |

### AI Providers

| Variable | Default | Description |
|---|---|---|
| `MCP_SERVER_PATH` | _(empty)_ | Path to the `mcp-server` binary. Required for MCP tools (`claude`, `qwen_code`, `gemini_cli`, and `codex_cli` providers). |
| `LLM_BASE_URL` | `https://api.openai.com/v1` | Base URL for the `llm` provider (any OpenAI-compat API) |
| `LLM_API_KEY` | _(empty)_ | API key for `llm` or `anthropic` provider |

### Other

| Variable | Default | Description |
|---|---|---|
| `GITHUB_SYNC_INTERVAL` | `30s` | How often to poll GitHub for PR status updates. Accepts Go duration strings (e.g. `1m`, `5m`). |
| `LOG_LEVEL` | `INFO` | Logging level: `DEBUG`, `INFO`, `WARN`, `ERROR` |

### YAML Config File

If `CONFIG_FILE` points to a YAML file, values from it are used as defaults (env vars override):

```yaml
port: "8080"
db_path: agent-task-editor.db
api_token: ""
cors_origins: "*"
mcp_server_path: /path/to/mcp-server
llm_base_url: https://api.openai.com/v1
llm_api_key: ""
max_workers: 5
repo_base_dir: /repos
upload_dir: /data/uploads
github_sync_interval: 30s
```

### Authentication

Set `API_TOKEN` to require an `Authorization: Bearer <token>` header on all API requests. WebSocket connections pass the token via `?token=<value>` since browsers cannot set custom headers on WebSocket upgrades.

The frontend reads `VITE_API_BASE_URL` and `VITE_WS_BASE_URL` at build time. For the Docker image these default to `""` (same origin). For local development add a `.env.local` in `frontend/`:

```
VITE_API_BASE_URL=http://localhost:8080
VITE_WS_BASE_URL=ws://localhost:8080
VITE_API_TOKEN=your-token-here
```

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

### Anthropic API (`anthropic` provider)

Set `LLM_API_KEY` to your Anthropic API key. No binary needed. See [providers/anthropic.md](providers/anthropic.md).

### OpenAI / LLM (`llm` provider)

Set `LLM_BASE_URL` and `LLM_API_KEY`. Works with any OpenAI-compatible API. See [providers/llm.md](providers/llm.md).

### Opencode (`opencode` provider)

Install the `opencode` binary and configure it. **MCP tools are not available.** See [providers/opencode.md](providers/opencode.md).

### Qwen Code (`qwen_code` provider)

Install the `qwen` binary (`npm i -g @qwen-code/qwen-code`, or build the backend image with `INSTALL_QWEN_CLI=true`). MCP tools are supported (same setup as `claude`). See [providers/qwen_code.md](providers/qwen_code.md).

### Gemini CLI (`gemini_cli` provider)

Install the `gemini` binary (`npm i -g @google/gemini-cli`, or build the backend image with `INSTALL_GEMINI_CLI=true`) and authenticate (Google account login or `GEMINI_API_KEY`). MCP tools are supported via a per-run isolated `GEMINI_CLI_HOME`. See [providers/gemini_cli.md](providers/gemini_cli.md).

### Codex CLI (`codex_cli` provider)

Install the `codex` binary (`npm i -g @openai/codex`, or build the backend image with `INSTALL_CODEX_CLI=true`) and authenticate (`codex login` with ChatGPT, or `OPENAI_API_KEY`). MCP tools are supported via a per-run isolated `CODEX_HOME`. See [providers/codex_cli.md](providers/codex_cli.md).

## Mounting Repositories

Agents run with their working directory set to the registered repo path. Mount the directories you want agents to access:

```yaml
volumes:
  - /path/to/your/projects:/repos:rw
```

Then register repos using paths under `/repos` in the UI (or via the API), and set `REPO_BASE_DIR=/repos` to prevent agents from escaping that subtree.

## Supported Languages & Extending the Toolchain

Agent `Bash`/`run_bash` tool calls execute *inside the backend container*, against your bind-mounted repos (see [Mounting Repositories](#mounting-repositories) above). That means any compiler, runtime, or build tool an agent needs to build, lint, or test a repo must be installed in `backend/Dockerfile`'s final runtime image — not just available on your host machine.

> **Note:** `frontend/Dockerfile` only builds and serves Agent Task Editor's own UI in production. It is not involved in running agent commands against your repos, so it doesn't need any of this.

### Currently supported out of the box

The backend image (`backend/Dockerfile`) ships with:

- **Go 1.26** — the same toolchain version used to build this project's own `server`/`mcp-server` binaries, copied from the Dockerfile's `golang:1.26-alpine` builder stage into the final image (rather than installed via `apk`) so the agent-visible `go version` always matches exactly. `GOPATH`/`GOCACHE`/`GOMODCACHE` are set to writable locations under `/home/node` for the non-root `node` user.
- **Node.js 26 / npm** — inherited from the `node:26-alpine` base image. Covers Vite, React, TypeScript projects and their usual workflows out of the box: `npm ci`, `npm run build`, `npm test`, `npx vitest`, etc.
- **`build-base`** (gcc, g++, make, musl-dev) — needed for `cgo` builds (this repo's own backend depends on `mattn/go-sqlite3`, which is cgo) and for any npm packages with native addons that need `node-gyp` compilation.
- **`git`, `bash`, `github-cli` (`gh`)** — for cloning, diffing, committing, and interacting with GitHub from inside agent runs.

### Adding more languages/compilers

To give agents the ability to build/test repos in another language, edit the **final stage** of `backend/Dockerfile` (the `FROM node:26-alpine` stage — don't touch the builder stage, which only compiles this project's own Go binaries) and add one of:

- **Alpine packages**, e.g.:
  ```dockerfile
  RUN apk add --no-cache python3 py3-pip   # Python
  RUN apk add --no-cache openjdk17         # Java
  RUN apk add --no-cache ruby ruby-dev     # Ruby
  RUN apk add --no-cache rustup            # Rust (then `rustup-init -y` as the node user)
  ```
- **Multi-stage `COPY --from=`**, the same technique used for Go above — pull a toolchain from an upstream image without bloating the final image with its own build dependencies:
  ```dockerfile
  FROM rust:1-alpine AS rust-builder
  ...
  FROM node:26-alpine
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

**Caveat — Alpine vs glibc:** Alpine uses `musl libc`, not `glibc`. Some toolchains/prebuilt binaries (certain Rust crates, precompiled CLI tools, some Python wheels) expect `glibc` and may fail to run or compile. Workarounds include installing `gcompat` (`apk add --no-cache gcompat`) for partial compatibility, or building from source instead of using a prebuilt glibc binary. If a toolchain is fundamentally incompatible with Alpine, consider whether it's worth switching `backend/Dockerfile`'s final stage to a Debian/Ubuntu-based Node image instead — this is a larger change with broader image-size and security tradeoffs, not something to do for a one-off need.

Remember to make sure any new tool's cache/config directories are writable by the `node` user. The container runs as `node`, which `backend/entrypoint.sh` remaps to your host `PUID`/`PGID` at startup and `chown`s the state dirs (`/data`, `/app`, the Go caches under `/home/node`) accordingly. If your tool writes elsewhere under `/home/node`, add it to that `chown` list — otherwise agents will hit permission errors the first time they invoke the tool.

## Local Development

### Backend

```bash
cd backend
go run ./cmd/server
```

Requires Go 1.24. The database file is created automatically with migrations applied on startup.

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

### Building the MCP Sidecar

The MCP sidecar enables the `signal_complete`, `request_human`, `update_task_notes`, and `store_info` tools for the `claude` and `qwen_code` providers:

```bash
cd backend
go build -o mcp-server ./cmd/mcp-server
```

Set `MCP_SERVER_PATH=/path/to/mcp-server` in the backend environment. `./dev.sh dev` does this automatically.

## First Steps After Startup

1. **Register a repository** — go to Settings → Repos → Add Repo. Enter the local filesystem path of the repository agents should work in.
2. **Create an agent config** — go to Settings → Agents → New Agent. Select a provider, enter a model name, set target labels (e.g. `["plan", "work"]`), and optionally write a system prompt.
3. **Create a task** — go to the Board, click New Task. Select the repo and fill in the title/description.
4. **Move it to `plan`** — drag it or use the label selector. The dispatcher will pick it up within 5 seconds and start an agent run.
5. **Watch the logs** — click on the task to open the detail view; live logs stream in real time.
