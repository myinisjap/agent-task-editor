# Provider: `qwen_code`

The `qwen_code` provider runs the Qwen Code CLI in headless mode. It has the same MCP tool support as the `claude` provider — both use the same stream-json output format and `mcp__<server>__<tool>` naming convention.

## Provider String

```
"provider": "qwen_code"
```

## How It Works

Runs: `qwen -p <prompt> --system-prompt <system> --output-format stream-json --approval-mode yolo --max-session-turns <max_turns> [--mcp-config <tempfile>] [--allowed-tools ...] [--exclude-tools ...]`

`<max_turns>` comes from the agent config's `max_turns` field (defaults to `50` when unset or `0`), mirroring the `claude` provider.

**Max-turns detection (verified against `@qwen-code/qwen-code` 0.25.2):** unlike `claude`, which emits a stream-json `result` message with `subtype:"error_max_turns"` when the cap is hit, qwen 0.25.2's `handleMaxTurnsExceededError` instead writes `Reached max session turns for this session. Increase the number of turns by specifying maxSessionTurns in settings.json.` to **stderr** and exits non-zero (`FatalTurnLimitedError`'s exit code). The runner matches that exact stderr line (see `internal/agent/errclass.go`'s `ClassMaxTurns` pattern) and treats it the same as claude's structural signal: the run is reported as `failed` with `agent.ErrMaxTurns`, which escalates the task to `waiting_human` instead of being retried or re-dispatched with a fresh turn budget. Any usage tokens from assistant messages emitted before the cap was hit are preserved in the recorded run.

Uses the same NDJSON stream-json parser as the `claude` provider (`classifyStreamJSON`). The MCP sidecar is launched and connected via `--mcp-config` when `MCP_SERVER_PATH` is set.

`QWEN_CODE_SUPPRESS_YOLO_WARNING=1` is automatically set in the environment to suppress headless mode warnings from the CLI.

## Credentials

The `qwen` binary must be installed and configured on the host (or container). Refer to Qwen Code's documentation for auth setup. No server-side API key is required (auth is managed by the binary).

The `qwen` CLI is not installed in the backend image by default — build with `INSTALL_QWEN_CLI=true` to have the backend `Dockerfile` `npm install -g @qwen-code/qwen-code` for you (see the backend `Dockerfile`'s `INSTALL_QWEN_CLI` build arg), or install it yourself with `npm i -g @qwen-code/qwen-code` and mount it into the container.

**Subprocess environment:** the `qwen` subprocess does not inherit the
backend's full environment (see [providers/README.md § Subprocess
environment](README.md#subprocess-environment)). It receives `PATH`/`HOME`
plus `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL`, `GEMINI_API_KEY`,
`GOOGLE_API_KEY` if set on the backend (plus the always-injected
`QWEN_CODE_SUPPRESS_YOLO_WARNING` above), a handful of locale/proxy/TLS
vars, and this agent config's own `env` field. Any other backend env var
will not reach the CLI.

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

## Effort

**Not supported.** The qwen CLI has no reasoning-effort flag or config
knob; the agent config's `effort` field is ignored for this provider. See
[agents.md § Effort](../agents.md#effort).

## Fallback Outcome Parsing

Like the `claude` provider, if the agent completes without calling `signal_complete`, the runner scans the final result text for `OUTCOME: success` or `OUTCOME: failure` as a fallback.

## Cost & Usage Reporting

Token usage is parsed from the CLI's `result` stream-json message (`usage`) via the same `classifyStreamJSON` parser as the `claude` provider, and the token counts themselves are used as-is (authoritative).

**No total-cost figure is reported by the Qwen Code CLI**, unlike `claude`. The parser also looks for `total_cost_usd` on the same envelope, but qwen's result message doesn't carry that field (verified against 0.25.2: its result-building path emits `usage` + `permission_denials` and the string `total_cost_usd` appears nowhere in the package). Rather than leaving `cost_usd` at `0` (which silently defeats pre-dispatch `max_cost_usd` budgets), the runner estimates it from the result's token usage via the same DB-backed `model_pricing` table (Settings → Pricing) that `codex_cli` uses — see `estimateCachedCostUSD` in `internal/agent/providers/pricing.go`. If `cache_read_input_tokens` is present on the result usage (verified against 0.25.2's `computeUsageFromMetrics`, which populates it from the CLI's own cached-token telemetry), that portion of the input tokens is billed at the pricing row's cached rate (`cached_input_per_1m`) instead of the full input rate.

**No price row for your Qwen model ⇒ `cost_unknown`, not a guessed price.** Qwen's own models (e.g. `qwen3-coder-plus`) are not in the seeded `model_pricing` table — add a row for the model(s) you actually use under **Settings → Pricing** to get a real estimate. Until you do, a run that consumed tokens is flagged `cost_unknown` (surfaced in the run history UI) rather than reporting a misleading `cost_usd = 0`. Do not invent a price for a model you can't verify; leave it unpriced and flagged instead.

**Model used for pricing.** If the agent config's `model` field is left empty (qwen picks its own default), the runner instead prices against the model named on the stream-json `assistant` message's `message.model` field (verified against 0.25.2's `StreamJsonOutputAdapter`, which sets it from `config.getModel()` on every assistant message) — the only structural source, since the result event itself carries no model field. If no assistant message was ever emitted either, pricing falls back to an empty model string, which is never in the pricing table, so the run is flagged `cost_unknown`.

**Mid-run cost kill switch: supported only when the configured (or discovered) model is priced.** `qwen_code` shares the same watchdog mechanism as `claude` (incremental token usage from `assistant` stream-json messages, projected via the pricing table). If the model isn't in the pricing table the watchdog can't project a cost and is a silent no-op; only the pre-dispatch budget guard applies. See [agents.md § Cost Budgets](../agents.md#cost-budgets).

## Setup Checklist

1. Install the `qwen` CLI (`npm i -g @qwen-code/qwen-code`) and add it to `PATH` (or mount it into the container; see the backend `Dockerfile`'s `INSTALL_QWEN_CLI` build arg)
2. Configure Qwen Code's auth via its own config
3. Set `MCP_SERVER_PATH` to the path of the built `mcp-server` binary
4. Create a [Provider Config](../agents.md#provider-configs) with `"provider": "qwen_code"`, then an agent config referencing it via `provider_config_id`
