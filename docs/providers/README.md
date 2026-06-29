# Providers

Each provider file describes the credentials, MCP support, limitations, and setup for a specific agent backend.

| Provider string | File | MCP Tools | CLI Binary | Notes |
|---|---|---|---|---|
| `claude` | [claude.md](claude.md) | ✅ All 5 | `claude` | Claude Max or API key; image attachments |
| `anthropic` | [anthropic.md](anthropic.md) | ❌ Native tools | None | Direct API; per-token billing |
| `opencode` | [opencode.md](opencode.md) | ❌ None | `opencode` | Text-based OUTCOME signalling only |
| `qwen_code` | [qwen_code.md](qwen_code.md) | ✅ All 5 | `qwen` | Same MCP support as `claude` |
| anything else | [llm.md](llm.md) | ❌ Native tools | None | OpenAI-compat API; Ollama, GPT, etc. |

## MCP Tool Availability by Provider

| Tool | `claude` | `anthropic` | `opencode` | `qwen_code` | `llm` |
|---|---|---|---|---|---|
| `get_task_transitions` | ✅ | — | ❌ | ✅ | — |
| `signal_complete` | ✅ MCP | ✅ native | ❌ text only | ✅ MCP | ✅ native |
| `request_human` | ✅ MCP | ✅ native | ❌ | ✅ MCP | ✅ native |
| `update_task_notes` | ✅ MCP | ✅ native | ❌ | ✅ MCP | ✅ native |
| `store_info` | ✅ MCP | ✅ native | ❌ | ✅ MCP | ✅ native |

"MCP" = via MCP sidecar (requires `MCP_SERVER_PATH`). "native" = built-in Go tool-use loop.
