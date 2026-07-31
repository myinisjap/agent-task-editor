# Providers

Each provider file describes the credentials, MCP support, limitations, and setup for a specific agent backend.

> **`anthropic` and `llm` are deprecated: disabled for now and may be removed in a future release.** New/updated
> provider configs using either are rejected by the API and neither is offered in the UI's provider dropdown.
> Existing configs already using them continue to dispatch and run. `llm` was previously also the catch-all
> backend for **any** unrecognized provider string (the "anything else" row this table used to have) — that
> fallback has been removed; an unrecognized provider string now fails the run explicitly instead of silently
> becoming an OpenAI-compatible call.

| Provider string | File | MCP Tools | CLI Binary | Notes |
|---|---|---|---|---|
| `claude` | [claude.md](claude.md) | ✅ All 5 | `claude` | Claude Max or API key; image attachments |
| `anthropic` **(deprecated)** | [anthropic.md](anthropic.md) | ❌ Native tools | None | Direct API; per-token billing |
| `opencode` | [opencode.md](opencode.md) | ❌ None | `opencode` | Text-based OUTCOME signalling only |
| `qwen_code` | [qwen_code.md](qwen_code.md) | ✅ All 5 | `qwen` | Same MCP support as `claude` |
| `codex_cli` | [codex_cli.md](codex_cli.md) | ✅ All 5 | `codex` | Per-run isolated `CODEX_HOME`; native sandbox/approval system |
| `llm` **(deprecated)** | [llm.md](llm.md) | ❌ Native tools | None | OpenAI-compat API; Ollama, GPT, etc. |

## MCP Tool Availability by Provider

| Tool | `claude` | `anthropic` (deprecated) | `opencode` | `qwen_code` | `codex_cli` | `llm` (deprecated) |
|---|---|---|---|---|---|---|
| `get_task_transitions` | ✅ | — | ❌ | ✅ | ✅ | — |
| `signal_complete` | ✅ MCP | ✅ native | ❌ text only | ✅ MCP | ✅ MCP | ✅ native |
| `request_human` | ✅ MCP | ✅ native | ❌ | ✅ MCP | ✅ MCP | ✅ native |
| `update_task_notes` | ✅ MCP | ✅ native | ❌ | ✅ MCP | ✅ MCP | ✅ native |
| `store_info` | ✅ MCP | ✅ native | ❌ | ✅ MCP | ✅ MCP | ✅ native |

"MCP" = via MCP sidecar (requires `MCP_SERVER_PATH`). "native" = built-in Go tool-use loop.
