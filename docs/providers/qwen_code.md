# Provider: `qwen_code`

The `qwen_code` provider runs the Qwen Code CLI in headless mode. It has the same MCP tool support as the `claude` provider — both use the same stream-json output format and `mcp__<server>__<tool>` naming convention.

## Provider String

```
"provider": "qwen_code"
```

## How It Works

Runs: `qwen -p <prompt> --system-prompt <system> --output-format stream-json --approval-mode yolo --max-session-turns <max_turns> [--mcp-config <tempfile>] [--allowed-tools ...]`

`<max_turns>` comes from the agent config's `max_turns` field (defaults to `50` when unset or `0`), mirroring the `claude` provider.

Uses the same NDJSON stream-json parser as the `claude` provider (`classifyStreamJSON`). The MCP sidecar is launched and connected via `--mcp-config` when `MCP_SERVER_PATH` is set.

`QWEN_CODE_SUPPRESS_YOLO_WARNING=1` is automatically set in the environment to suppress headless mode warnings from the CLI.

## Credentials

The `qwen` binary must be installed and configured on the host (or container). Refer to Qwen Code's documentation for auth setup. No server-side API key is required (auth is managed by the binary).

The `qwen` CLI is not installed in the backend image by default — build with `INSTALL_QWEN_CLI=true` to have the backend `Dockerfile` `npm install -g @qwen-code/qwen-code` for you (see the backend `Dockerfile`'s `INSTALL_QWEN_CLI` build arg), or install it yourself with `npm i -g @qwen-code/qwen-code` and mount it into the container.

## MCP Tools

**All 5 MCP tools are supported** when `MCP_SERVER_PATH` is set — identical to the `claude` provider.

| Tool | Description |
|---|---|
| `mcp__task-editor__get_task_transitions` | Returns available workflow transitions |
| `mcp__task-editor__signal_complete` | Marks the run done with `success` or `failure` |
| `mcp__task-editor__request_human` | Pauses the run for human input |
| `mcp__task-editor__update_task_notes` | Writes persistent notes for subsequent agents |
| `mcp__task-editor__store_info` | Stores a summary visible in the task UI |

Qwen uses `--allowed-tools` (space-separated, multiple flags) rather than a comma-separated string like Claude:

```
--allowed-tools mcp__task-editor__get_task_transitions
--allowed-tools mcp__task-editor__signal_complete
--allowed-tools mcp__task-editor__request_human
--allowed-tools mcp__task-editor__update_task_notes
--allowed-tools mcp__task-editor__store_info
```

See [mcp-tools.md](../mcp-tools.md) for full tool reference.

## Image Attachments

Not yet supported. Reserved for when the `qwen` CLI gains an `--image` flag.

## Command Allowlist / Denylist

`command_allowlist` patterns are enforced natively by the `qwen` CLI: each pattern is
appended as a `Bash(pattern)` entry to `--allowed-tools`, the same tool-restriction
syntax the `claude` CLI's `--allowedTools` accepts.

`command_denylist` is **not currently enforced** for this provider — there is no
confirmed `qwen` CLI flag equivalent to claude's `--disallowedTools` /
`permissions.deny` settings key. If you need denylist enforcement, prefer the
`claude`, `anthropic`, or `llm` providers, or rely solely on `command_allowlist` here.

## Model Selection

Pass `model` in the agent config. It is passed via `--model <model>` to the CLI.

## Fallback Outcome Parsing

Like the `claude` provider, if the agent completes without calling `signal_complete`, the runner scans the final result text for `OUTCOME: success` or `OUTCOME: failure` as a fallback.

## Cost & Usage Reporting

Like the `claude` provider, token usage and cost are parsed from the CLI's `result` stream-json message (`usage` + `total_cost_usd`) via the same `classifyStreamJSON` parser, and are used as-is (not estimated) — assuming the `qwen` CLI's stream-json output stays compatible with `claude`'s. See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

## Setup Checklist

1. Install the `qwen` CLI (`npm i -g @qwen-code/qwen-code`) and add it to `PATH` (or mount it into the container; see the backend `Dockerfile`'s `INSTALL_QWEN_CLI` build arg)
2. Configure Qwen Code's auth via its own config
3. Set `MCP_SERVER_PATH` to the path of the built `mcp-server` binary
4. Create an agent config with `"provider": "qwen_code"`
