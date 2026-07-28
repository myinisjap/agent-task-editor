# Provider: `qwen_code`

The `qwen_code` provider runs the Qwen Code CLI in headless mode. It has the same MCP tool support as the `claude` provider — both use the same stream-json output format and `mcp__<server>__<tool>` naming convention.

## Provider String

```
"provider": "qwen_code"
```

## How It Works

Runs: `qwen -p <prompt> --system-prompt <system> --output-format stream-json --approval-mode yolo --max-session-turns <max_turns> [--mcp-config <tempfile>] [--allowed-tools ...] [--exclude-tools ...]`

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

**`command_allowlist` is not enforced for this provider.** It is intentionally
not translated to any CLI flag. qwen's `--allowed-tools` documents itself as
*"Tools to allow, will bypass confirmation"* — an auto-approve list, exactly
like the `claude` CLI's `--allowedTools` — it does not block non-matching
commands. On top of that, this runner always passes `--approval-mode yolo`
(*"auto-approve all tools"*), which auto-approves everything anyway, so an
allowlist would have no effect even if wired up. Use `command_denylist`
instead if you need enforced restrictions.

**`command_denylist` is enforced via `--exclude-tools`.** Each denylist pattern
is appended as a `Bash(pattern)` entry to `--exclude-tools`, e.g.
`--exclude-tools Bash(rm -rf *)`. qwen folds `--exclude-tools` into its
`permissionsDeny` policy, which is honored even under `--approval-mode yolo`.

> **Known uncertainty:** the `Bash(pattern)` glob shape is confirmed for
> `--allowed-tools`, but has not been verified against a live authenticated
> qwen run for `--exclude-tools` specifically. If qwen's deny path only
> accepts bare tool names (rather than `Bash(pattern)` sub-matching), a
> per-pattern denylist entry may silently fail to match anything, and a
> blanket `--exclude-tools Bash` (denying all Bash calls, no pattern) would be
> the only reliably available granularity. Treat per-pattern denial as
> best-effort until confirmed live.

_Verified against `@qwen-code/qwen-code` v0.21.0's registered CLI options._

## Session Resume

The CLI's stream-json result message carries a `session_id`, which the runner records. When a later run on the same task hits this same agent config (and the config's `resume_sessions` flag is on — the default), the runner invokes the CLI with `--resume <session_id>` (qwen 0.21.0 registers `-r, --resume <string>`, *"Resume a specific session by its ID"*), so prior context (transcript, task-editor tool history, rejection feedback, open review comments) carries forward instead of starting cold.

## Model Selection

Pass `model` on the referenced [Provider Config](../agents.md#provider-configs). It is passed via `--model <model>` to the CLI.

## Fallback Outcome Parsing

Like the `claude` provider, if the agent completes without calling `signal_complete`, the runner scans the final result text for `OUTCOME: success` or `OUTCOME: failure` as a fallback.

## Cost & Usage Reporting

Token usage is parsed from the CLI's `result` stream-json message (`usage`) via the same `classifyStreamJSON` parser as the `claude` provider, and is used as-is (not estimated).

**No total-cost figure is reported by the Qwen Code CLI**, unlike `claude`. The parser also looks for `total_cost_usd` on the same envelope, but qwen's result message doesn't carry that field (verified against v0.21.0: its `buildResultMessage` emits `usage` + `permission_denials` and the string `total_cost_usd` appears nowhere in the package), so `cost_usd` is left at `0` for this provider rather than estimated — the same situation as `codex_cli`. A cost budget cap will not reliably fire here. See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

## Setup Checklist

1. Install the `qwen` CLI (`npm i -g @qwen-code/qwen-code`) and add it to `PATH` (or mount it into the container; see the backend `Dockerfile`'s `INSTALL_QWEN_CLI` build arg)
2. Configure Qwen Code's auth via its own config
3. Set `MCP_SERVER_PATH` to the path of the built `mcp-server` binary
4. Create a [Provider Config](../agents.md#provider-configs) with `"provider": "qwen_code"`, then an agent config referencing it via `provider_config_id`
