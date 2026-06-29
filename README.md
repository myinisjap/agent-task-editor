# Agent Task Editor

> A self-hosted Kanban board where AI agents automatically work through tasks as they move across workflow columns.

Agent Task Editor lets you define custom workflows, assign AI agents to specific columns, and have those agents automatically dispatch, run, and report back — all tracked against your code repositories. Think of it as a CI pipeline, but driven by LLMs instead of scripts, with a human-in-the-loop approval gate wherever you want one.

Each task moves through a directed state machine (the *workflow*). When a task lands on a label that an AI agent is configured to handle, the dispatcher picks it up within 5 seconds, creates a run record, streams live logs to the UI, and transitions the task to the next label when the agent signals it's done. Human-gated transitions require an explicit Approve or Reject click before the task moves on.

---

## Features at a Glance

- **Kanban board** with drag-and-drop between columns
- **Live log streaming** — agent stdout, tool calls, and tool results streamed in real time
- **Log replay** — reconnecting clients receive all prior logs for the current run
- **Workflow editor** — create/edit labels, transitions, and trigger types; import/export YAML
- **Agent config UI** — manage multiple AI configs, each targeting different workflow stages
- **Git diff viewer** — per-repo diff of uncommitted changes
- **Dashboard** — run counts, completion rate, and recent activity
- **Bearer token auth** — optional `API_TOKEN`; WebSocket auth via `?token=` query param
- **Docker Compose deployment** — single `docker compose up` to run everything

---

## Quick Start

```bash
git clone https://github.com/myinisjap/agent-task-editor
cd agent-task-editor
docker compose up -d
```

Open **http://localhost:5173** in your browser.

### Mount the Claude CLI (if using the `claude` provider)

The default `docker-compose.yml` shares your existing Claude auth with the container:

```yaml
volumes:
  - ${HOME}/.claude:/home/appuser/.claude   # auth credentials
```

The `claude` CLI binary itself is baked into the backend image — you don't need to mount it from the host. You do need to authenticate on your host machine (`claude login`) so the credentials are present at `~/.claude` before starting the stack.

### Mount your repositories

Agents run with their working directory set to the registered repo path. Add a volume for the projects you want agents to access:

```yaml
volumes:
  - /path/to/your/projects:/repos:rw
```

Then set `REPO_BASE_DIR=/repos` in the backend environment to prevent agents from accessing paths outside that subtree.

See [docs/getting-started.md](docs/getting-started.md) for the full setup guide, all environment variables, and local development instructions.

---

## First Steps After Startup

1. **Register a repository** — Settings → Repos → Add Repo. Enter the local filesystem path agents should work in.
2. **Create an agent config** — Settings → Agents → New Agent. Select a provider, enter a model, set target labels (e.g. `["todo", "in-progress"]`), and optionally write a system prompt.
3. **Create a task** — Board → New Task. Select the repo and fill in the title and description.
4. **Move it to `todo`** — drag it or use the label selector. The dispatcher picks it up within 5 seconds.
5. **Watch the logs** — click the task to open the detail view; logs stream live as the agent works.

---

## Agent Provider Comparison

Three providers are available. Choose based on your auth setup, billing preference, and tool requirements.

| Provider | Auth Required | CLI Dependency | Built-in Tools | Label Transitions | Notes |
|---|---|---|---|---|---|
| **`claude`** | Claude Max subscription (authenticated via `~/.claude`) | ✅ `claude` CLI must be installed & authenticated on the host/container | `Edit`, `Write`, `Read`, `Bash`, `Glob`, `Grep` + MCP tools | ✅ via MCP sidecar (`MCP_SERVER_PATH` must be set) | Without MCP, runs always complete with no label transition. Dangerous env vars (`PATH`, `LD_PRELOAD`, `HOME`, `SHELL`, etc.) are blocked for security. |
| **`anthropic`** | Anthropic API key (`LLM_API_KEY`) | ❌ No CLI needed | `read_file`, `write_file`, `run_bash`, `signal_complete`, `request_human` | ✅ Built-in (no MCP needed) | Billed per-token — separate from a Claude Max subscription. No `Glob`/`Grep` tools. |
| **`llm`** | API key (`LLM_API_KEY`) + `LLM_BASE_URL` | ❌ No CLI needed | `read_file`, `write_file`, `run_bash`, `signal_complete`, `request_human` | ✅ Built-in (no MCP needed) | Works with OpenAI, Azure OpenAI, Ollama, LM Studio, and any OpenAI-compatible endpoint. Same tool set as `anthropic`. Output quality varies by model/endpoint. |

### Key limitations to be aware of

- **`claude` without MCP** — If `MCP_SERVER_PATH` is not set, the agent has no way to call `signal_complete` or `request_human`. Every run will finish with status `completed` but the task label will **not** change. Always build and configure the MCP sidecar when using the `claude` provider.
- **`claude` env var restrictions** — The `env` field in agent configs cannot override system-critical variables (`PATH`, `LD_PRELOAD`, `HOME`, `SHELL`, and others). Attempts are blocked and logged as warnings.
- **`llm` model quality** — The `llm` provider sends the same tool definitions and prompts regardless of endpoint, but smaller or instruction-tuned models may not reliably follow tool-use conventions. Test your chosen model before relying on it for automated workflows.

---

## Security

> **This tool executes arbitrary shell commands by design.** AI agents run `Bash` (claude provider) or `run_bash` (anthropic/llm providers) with full shell access as the server user. The security boundary is the container and the `REPO_BASE_DIR` mount — not the application itself.

**Default settings are for localhost only.** Before exposing this to any non-localhost network:

- [ ] Set `API_TOKEN` — without it, anyone who can reach port 8080 can create repos, dispatch agents, and run shell commands
- [ ] Set `REPO_BASE_DIR` — without it, agents can be pointed at any path on the host
- [ ] Set `CORS_ORIGINS` to your actual origin instead of `*`
- [ ] Run behind a reverse proxy or VPN; do not expose port 8080 directly to the internet

The server binds to all interfaces (`:8080`) by default. In Docker, map it to `127.0.0.1:8080` if you don't need external access.

---

## Key Environment Variables

| Variable | Default | Description |
|---|---|---|
| `API_TOKEN` | _(empty)_ | Bearer token for API auth; empty = no auth required |
| `REPO_BASE_DIR` | _(empty)_ | Restrict repo registration to paths under this directory |
| `MCP_SERVER_PATH` | _(empty)_ | Path to the `mcp-server` binary; required for label transitions with the `claude` provider |
| `LLM_API_KEY` | _(empty)_ | API key for the `anthropic` or `llm` provider |
| `LLM_BASE_URL` | `https://api.openai.com/v1` | Base URL for the `llm` (OpenAI-compatible) provider |
| `MAX_WORKERS` | `5` | Maximum number of concurrent agent runs |

See [docs/getting-started.md](docs/getting-started.md) for the full variable reference.

---

## Local Development

The fastest path is the dev helper script:

```bash
./dev.sh dev   # starts backend + frontend + builds the MCP sidecar
```

Or run services individually:

```bash
# Backend (Go 1.24+)
cd backend && go run ./cmd/server

# Frontend (Node 20+)
cd frontend && npm install && npm run dev

# MCP sidecar (needed for claude provider label transitions)
cd backend && go build -o mcp-server ./cmd/mcp-server
```

---

## Documentation

| Doc | Description |
|---|---|
| [docs/overview.md](docs/overview.md) | Core concepts, architecture diagram, default workflow |
| [docs/getting-started.md](docs/getting-started.md) | Installation, environment variables, local dev setup |
| [docs/workflows.md](docs/workflows.md) | State machine format, labels, transitions, YAML import/export |
| [docs/agents.md](docs/agents.md) | Providers, dispatcher, worker pool, run lifecycle, prompt construction |
| [docs/api.md](docs/api.md) | Full REST API endpoint reference |
| [docs/websocket.md](docs/websocket.md) | Live log streaming WebSocket protocol |

---

## License

MIT — see [LICENSE](LICENSE).
