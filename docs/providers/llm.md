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

Makes HTTP calls to `POST <LLM_BASE_URL>/chat/completions` using the OpenAI chat completions format. Runs a tool-use loop (up to `max_turns` turns, default `50`) until the model calls `signal_complete` or `request_human`, or runs out of turns. The turn limit is configurable per agent config via the `max_turns` field (`0`/unset falls back to `50`).

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

## Command Allowlist / Denylist

`command_allowlist` and `command_denylist` (JSON arrays of `"*"`-wildcard glob
patterns on the agent config, both defaulting to `[]`/no restriction) are enforced
server-side, in Go, immediately before a `run_bash` call is executed: the denylist is
checked first and always wins; if the allowlist is non-empty, the command must also
match at least one allow pattern. A denied command returns an `error: ...` string to
the model instead of running.

This is **best-effort string matching against the full command line, not a sandbox**
— it does not prevent constructing a denied command indirectly (e.g. via `$()`,
backticks, string concatenation, or encoded payloads). It reduces the blast radius of
a straightforwardly misbehaving or prompt-injected agent, not a determined one.

## Image Attachments

Not supported.

## Model Selection

Pass `model` in the agent config (e.g. `gpt-4o`, `gpt-4o-mini`, `llama3.2`). Passed directly to the API.

## Cost & Usage Reporting

Token usage (`input_tokens`/`output_tokens`) is summed from the response's OpenAI-compatible `usage` field (`prompt_tokens`/`completion_tokens`) across every turn of the tool-use loop; `cost_usd` is an *estimate* computed from those tokens via the internal pricing table (`internal/agent/pricing.go`) — accuracy depends on the model ID matching an entry in that table. See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

## Rate Limit Handling

Detects HTTP 429 responses. Reads `x-ratelimit-reset-requests`, `x-ratelimit-reset-tokens`, and `retry-after` headers to determine when to retry.

## Setup Checklist

1. Set `LLM_BASE_URL` and `LLM_API_KEY`
2. Create an agent config with `"provider": "llm"` (or any non-reserved string) and the desired `model`
