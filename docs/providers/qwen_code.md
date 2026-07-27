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

**All 6 MCP tools are supported** (7 with `create_subtask`, which is exposed only when the agent config enables subtasks) when `MCP_SERVER_PATH` is set — identical to the `claude` provider.

| Tool | Description |
|---|---|
| `mcp__task-editor__get_task_transitions` | Returns available workflow transitions |
| `mcp__task-editor__signal_complete` | Marks the run done with `success` or `failure` |
| `mcp__task-editor__request_human` | Pauses the run for human input |
| `mcp__task-editor__update_task_notes` | Writes persistent notes for subsequent agents |
| `mcp__task-editor__store_info` | Stores a summary visible in the task UI |
| `mcp__task-editor__resolve_comment` | Marks an open inline review comment as addressed |
| `mcp__task-editor__create_subtask` | Splits the task into a child task (only exposed when the agent config has `subtasks_enabled`) |

Qwen uses `--allowed-tools` (space-separated, multiple flags) rather than a comma-separated string like Claude:

```
--allowed-tools mcp__task-editor__get_task_transitions
--allowed-tools mcp__task-editor__signal_complete
--allowed-tools mcp__task-editor__request_human
--allowed-tools mcp__task-editor__update_task_notes
--allowed-tools mcp__task-editor__store_info
--allowed-tools mcp__task-editor__resolve_comment
```

See [mcp-tools.md](../mcp-tools.md) for full tool reference.

## Image Attachments

Not yet supported. Reserved for when the `qwen` CLI gains an `--image` flag.

## Command Allowlist / Denylist

**Neither is enforced for this provider.**

`command_allowlist` patterns are appended as `Bash(pattern)` entries to
`--allowed-tools`, but that flag does not restrict anything: qwen documents it as
*"Tools to allow, will bypass confirmation"* — an auto-approve list, exactly like
the `claude` CLI's `--allowedTools`. On top of that, this runner always passes
`--approval-mode yolo` (*"auto-approve all tools"*), which auto-approves
everything anyway, so the allowlist entries are a complete no-op here.

`command_denylist` is **not currently enforced** either, because the runner never
passes a deny flag. Note that qwen *does* have one: `--exclude-tools` ("Tools to
exclude"), which the CLI folds into its `permissionsDeny` policy. Wiring
`command_denylist` through to `--exclude-tools` would close this gap; until then,
prefer the `claude` (denylist only), `anthropic`, or `llm` providers if you need
enforced command restrictions.

_Verified against `@qwen-code/qwen-code` v0.21.0's registered CLI options._

## Model Selection

Pass `model` on the referenced [Provider Config](../agents.md#provider-configs). It is passed via `--model <model>` to the CLI.

## Fallback Outcome Parsing

Like the `claude` provider, if the agent completes without calling `signal_complete`, the runner scans the final result text for `OUTCOME: success` or `OUTCOME: failure` as a fallback.

## Cost & Usage Reporting

Token usage is parsed from the CLI's `result` stream-json message (`usage`) via the same `classifyStreamJSON` parser as the `claude` provider, and is used as-is (not estimated).

**No total-cost figure is reported by the Qwen Code CLI**, unlike `claude`. The parser also looks for `total_cost_usd` on the same envelope, but qwen's result message doesn't carry that field (verified against v0.21.0: its `buildResultMessage` emits `usage` + `permission_denials` and the string `total_cost_usd` appears nowhere in the package), so `cost_usd` is left at `0` for this provider rather than estimated — the same situation as `gemini_cli`/`codex_cli`. A cost budget cap will not reliably fire here. See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

## Setup Checklist

1. Install the `qwen` CLI (`npm i -g @qwen-code/qwen-code`) and add it to `PATH` (or mount it into the container; see the backend `Dockerfile`'s `INSTALL_QWEN_CLI` build arg)
2. Configure Qwen Code's auth via its own config
3. Set `MCP_SERVER_PATH` to the path of the built `mcp-server` binary
4. Create a [Provider Config](../agents.md#provider-configs) with `"provider": "qwen_code"`, then an agent config referencing it via `provider_config_id`
