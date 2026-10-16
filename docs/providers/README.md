# Providers

Each provider file describes the credentials, MCP support, limitations, and setup for a specific agent backend.

> **`anthropic` and `llm` were removed in v0.17.0.** New/updated provider configs using either are rejected by the
> API. Legacy configs that still reference them are kept but fail at dispatch with a clear error (the task escalates
> to `waiting_human`) and show up as an error on the Health page — switch them to a supported provider. See
> [anthropic.md](anthropic.md) and [llm.md](llm.md). An unrecognized provider string fails the run the same way.

| Provider string | File | MCP Tools | CLI Binary | Notes |
|---|---|---|---|---|
| `claude` | [claude.md](claude.md) | ✅ All 5 | `claude` | Claude Max or API key; image attachments |
| `opencode` | [opencode.md](opencode.md) | ✅ All 5 | `opencode` | MCP via inline `OPENCODE_CONFIG_CONTENT` (no `--mcp-config` flag); text OUTCOME marker kept as a fallback |
| `qwen_code` | [qwen_code.md](qwen_code.md) | ✅ All 5 | `qwen` | Same MCP support as `claude` |
| `codex_cli` | [codex_cli.md](codex_cli.md) | ✅ All 5 | `codex` | Per-run isolated `CODEX_HOME`; native sandbox/approval system |

## MCP Tool Availability by Provider

| Tool | `claude` | `opencode` | `qwen_code` | `codex_cli` |
|---|---|---|---|---|
| `get_task_transitions` | ✅ | ✅ | ✅ | ✅ |
| `signal_complete` | ✅ MCP | ✅ MCP | ✅ MCP | ✅ MCP |
| `request_human` | ✅ MCP | ✅ MCP | ✅ MCP | ✅ MCP |
| `update_task_notes` | ✅ MCP | ✅ MCP | ✅ MCP | ✅ MCP |
| `store_info` | ✅ MCP | ✅ MCP | ✅ MCP | ✅ MCP |

"MCP" = via MCP sidecar (requires `MCP_SERVER_PATH`).

## Subprocess environment

The CLI-based providers (`claude`, `qwen_code`, `codex_cli`, `opencode`) do **not**
inherit the backend process's full environment. Each provider subprocess only
receives:

- A small **per-provider allowlist** pulled from the backend's own
  environment — `PATH`/`HOME` (so the CLI can be found and can read its own
  credentials/config), a few locale/proxy/TLS-trust vars, and that provider's
  specific auth vars (e.g. `ANTHROPIC_API_KEY` for `claude`, `OPENAI_API_KEY`
  for `codex_cli`). See each provider's own "Credentials" section for the
  exact list, or `backend/internal/agent/providers/cli.go` for the
  authoritative allowlists.
- Whatever the agent config's own `env` field sets explicitly (filtered
  through a small execution-hijack denylist — `PATH`, `HOME`, `NODE_OPTIONS`, `BASH_ENV`, `LD_*`, `DYLD_*`, `GIT_*`,
  interpreter-path vars, CA-bundle vars,
  etc. can't be overridden this way).

This means a backend-only secret (e.g. the API's own `API_TOKEN`) is never visible to
an agent subprocess unless it happens to also be on that provider's
allowlist. If an agent needs a value that isn't on its provider's allowlist,
set it via that agent config's `env` field rather than relying on it being
present in the backend's environment.

The same allowlisting applies to the **interactive chat terminal**
(`TerminalManager`, the `/chat/sessions/{id}/terminal` WebSocket) — it
launches the same `claude`/`codex`/`qwen`/`opencode` binaries with the same
Bash access, so it's scoped via the same per-provider allowlist
(`providers.EnvAllowlistFor`, wired in via `TerminalManager.EnvAllowlist`
from `cmd/server`) rather than the backend's full environment.
