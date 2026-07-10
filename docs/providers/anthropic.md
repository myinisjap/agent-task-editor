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

**Not supported.** No `--mcp-config` flag is used. Instead, equivalent functionality is provided natively, with the same call shape as the MCP version:

| Native Tool | Equivalent MCP Tool |
|---|---|
| `get_task_transitions()` | `mcp__task-editor__get_task_transitions` |
| `signal_complete(outcome, summary)` | `mcp__task-editor__signal_complete` |
| `request_human(message)` | `mcp__task-editor__request_human` |
| `update_task_notes(notes, append?)` | `mcp__task-editor__update_task_notes` |
| `store_info(info)` | `mcp__task-editor__store_info` |

`resolve_comment` and `create_subtask` are not available natively. See [agents.md](../agents.md) for full MCP-vs-native parity across providers.

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

Not yet supported.

## Model Selection

Pass `model` in the agent config (e.g. `claude-sonnet-4-6`, `claude-opus-4`). Defaults to `claude-sonnet-4-6` if not set.

## Cost & Usage Reporting

Token usage (`input_tokens`/`output_tokens`) is summed from the Messages API's `usage` field across every turn of the tool-use loop; `cost_usd` is an *estimate* computed from those tokens via the internal pricing table (`internal/agent/pricing.go`). See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

## When to Use

- Direct API access without the Claude CLI installed
- Environments where the CLI isn't available (e.g. minimal containers)
- Per-token billing scenarios (not Claude Max)

## Setup Checklist

1. Set `LLM_API_KEY` to your Anthropic API key
2. Create an agent config with `"provider": "anthropic"`
3. `MCP_SERVER_PATH` is not needed for this provider
