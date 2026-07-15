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

The provider string and model come from the agent config's referenced [Provider Config](../agents.md#provider-configs) (`provider_config_id`), not the agent config itself.

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

**Not supported.** No CLI binary is invoked, so no `--mcp-config` is possible. Equivalent functionality — including `get_task_transitions` and `signal_complete(outcome, summary)` with the same `outcome: "success"|"failure"` shape as MCP — is provided natively via the tool-use loop. `resolve_comment` and `create_subtask` are not available natively. See [agents.md](../agents.md) for full MCP-vs-native parity across providers.

## Native Tools Available to Agent

| Tool | Description |
|---|---|
| `read_file(path)` | Read a file from the repo |
| `write_file(path, content)` | Write/overwrite a file |
| `str_replace(path, old, new)` | Replace a substring in a file; `old` must match exactly once, or the call fails |
| `list_files(path?)` | List a single directory's immediate contents |
| `list_dir(path?)` | Recursively list files/dirs under path (skips `.git`, `node_modules`, and other dotdirs; capped at 2000 entries) |
| `search(pattern, glob?)` | Search the repo with ripgrep (`rg`), optionally restricted to files matching `glob`; capped at 1&nbsp;MB of output |
| `run_bash(command)` | Run a shell command |
| `get_task_transitions()` | List available workflow transitions from the task's current label |
| `store_info(info)` | Store run summary |
| `update_task_notes(notes, append?)` | Write agent notes |
| `signal_complete(outcome, summary)` | Complete the run; `outcome` is `"success"` or `"failure"` and the label is resolved automatically, same as MCP |
| `request_human(message)` | Pause for human input |

`search` requires `ripgrep` (`rg`) on `PATH`; the backend Docker image installs it by default. If `rg` isn't found, the tool returns `error: ripgrep (rg) not found on PATH` rather than failing the run. `search`, `list_dir`, and `list_files` are read-only and are **not** gated by the command allowlist/denylist (same treatment as `read_file`); `run_bash` is the only tool subject to that policy.

## Command Allowlist / Denylist

`command_allowlist` and `command_denylist` (JSON arrays of `"*"`-wildcard glob
patterns on the agent config — workflow-behavior fields, not part of the
Provider Config — both defaulting to `[]`/no restriction) are enforced
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

Pass `model` on the referenced [Provider Config](../agents.md#provider-configs) (e.g. `gpt-4o`, `gpt-4o-mini`, `llama3.2`). Passed directly to the API.

## Cost & Usage Reporting

Token usage (`input_tokens`/`output_tokens`) is summed from the response's OpenAI-compatible `usage` field (`prompt_tokens`/`completion_tokens`) across every turn of the tool-use loop; `cost_usd` is an *estimate* computed from those tokens via the internal pricing table (`internal/agent/pricing.go`) — accuracy depends on the model ID matching an entry in that table. See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

## Rate Limit Handling

Detects HTTP 429 responses. Reads `x-ratelimit-reset-requests`, `x-ratelimit-reset-tokens`, and `retry-after` headers to determine when to retry.

## Setup Checklist

1. Set `LLM_BASE_URL` and `LLM_API_KEY`
2. Create a [Provider Config](../agents.md#provider-configs) with `"provider": "llm"` (or any non-reserved string) and the desired `model`, then an agent config referencing it via `provider_config_id`
