# Provider: `anthropic`

The `anthropic` provider calls the Anthropic Messages API directly — no CLI binary required. It uses a native Go tool-use loop instead of the MCP sidecar.

## Provider String

```
"provider": "anthropic"
```

## How It Works

Makes direct HTTP calls to `https://api.anthropic.com/v1/messages` in a multi-turn tool-use loop (up to `max_turns` turns, default `50`). Tool calls are executed server-side in Go; the model is sent tool results and continues until it calls `signal_complete` or `request_human`, or runs out of turns. The turn limit is configurable per agent config via the `max_turns` field (`0`/unset falls back to `50`).

## Credentials

**Required:** `LLM_API_KEY` set to an Anthropic API key.

```bash
LLM_API_KEY=sk-ant-...
```

This is billed per-token — separate from a Claude Max subscription. The `claude` provider is billed under Claude Max; `anthropic` is billed as API usage.

## MCP Tools

**Not supported.** No `--mcp-config` flag is used. Instead, equivalent functionality is provided natively:

| Native Tool | Equivalent MCP Tool |
|---|---|
| `signal_complete(next_label, summary)` | `mcp__task-editor__signal_complete` |
| `request_human(message)` | `mcp__task-editor__request_human` |
| `update_task_notes(notes, append?)` | `mcp__task-editor__update_task_notes` |
| `store_info(info)` | `mcp__task-editor__store_info` |

**Note:** The `anthropic` provider's `signal_complete` tool takes a `next_label` parameter (the exact label name to move to), unlike the MCP version which takes `outcome: "success"|"failure"` and resolves the label automatically.

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

## Image Attachments

Not yet supported.

## Model Selection

Pass `model` in the agent config (e.g. `claude-sonnet-4-6`, `claude-opus-4`). Defaults to `claude-sonnet-4-6` if not set.

## Cost & Usage Reporting

Token usage (`input_tokens`/`output_tokens`) is summed from the Messages API's `usage` field across every turn of the tool-use loop; `cost_usd` is an *estimate* computed from those tokens via the internal pricing table (`internal/agent/pricing.go`). See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

## When to Use

- Direct API access without the Claude CLI installed
- Environments where the CLI isn't available (e.g. minimal containers)
- Per-token billing scenarios (not Claude Max)
- When you need precise control over which label to transition to (via `next_label`)

## Setup Checklist

1. Set `LLM_API_KEY` to your Anthropic API key
2. Create an agent config with `"provider": "anthropic"`
3. `MCP_SERVER_PATH` is not needed for this provider
