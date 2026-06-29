# Provider: `llm` (OpenAI-Compatible API)

The `llm` provider calls any OpenAI-compatible REST endpoint. It's the catch-all for any provider string that doesn't match `claude`, `anthropic`, `opencode`, or `qwen_code`. Like the `anthropic` provider, it uses a native Go tool-use loop — no CLI binary needed.

## Provider String

Any string that doesn't match the specific provider names — convention is to use `llm`, `openai`, or a descriptive name like `custom`:

```
"provider": "llm"
"provider": "openai"
"provider": "custom"
```

## How It Works

Makes HTTP calls to `POST <LLM_BASE_URL>/chat/completions` using the OpenAI chat completions format. Runs a tool-use loop (up to 50 turns) until the model calls `signal_complete` or `request_human`, or runs out of turns.

## Credentials

**Required:**
- `LLM_API_KEY` — sent as `Authorization: Bearer <key>`
- `LLM_BASE_URL` — defaults to `https://api.openai.com/v1`; override to point at any compatible API

```bash
LLM_BASE_URL=https://api.openai.com/v1
LLM_API_KEY=sk-...
```

## Compatible Backends

| Backend | `LLM_BASE_URL` |
|---|---|
| OpenAI | `https://api.openai.com/v1` (default) |
| Azure OpenAI | `https://<resource>.openai.azure.com/openai/deployments/<model>` |
| Ollama (local) | `http://localhost:11434/v1` |
| LM Studio | `http://localhost:1234/v1` |
| Any OpenAI-compat API | Set accordingly |

## MCP Tools

**Not supported.** No CLI binary is invoked, so no `--mcp-config` is possible. Equivalent functionality is provided natively via the tool-use loop.

## Native Tools Available to Agent

| Tool | Description |
|---|---|
| `read_file(path)` | Read a file from the repo |
| `write_file(path, content)` | Write/overwrite a file |
| `run_bash(command)` | Run a shell command |
| `list_files(path?)` | List directory contents |
| `store_info(info)` | Store run summary |
| `update_task_notes(notes, append?)` | Write agent notes |
| `signal_complete(next_label, summary)` | Complete the run |
| `request_human(message)` | Pause for human input |

**Note:** `signal_complete` here takes `next_label` (the exact label name), unlike the MCP version which takes `outcome: "success"|"failure"`.

## Image Attachments

Not supported.

## Model Selection

Pass `model` in the agent config (e.g. `gpt-4o`, `gpt-4o-mini`, `llama3.2`). Passed directly to the API.

## Rate Limit Handling

Detects HTTP 429 responses. Reads `x-ratelimit-reset-requests`, `x-ratelimit-reset-tokens`, and `retry-after` headers to determine when to retry.

## Setup Checklist

1. Set `LLM_BASE_URL` and `LLM_API_KEY`
2. Create an agent config with `"provider": "llm"` (or any non-reserved string) and the desired `model`
