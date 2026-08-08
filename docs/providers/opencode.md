# Provider: `opencode`

The `opencode` provider runs the [opencode](https://opencode.ai) CLI. **MCP tools are not supported** — opencode has no `--mcp-config` flag. Agents must signal completion by printing `OUTCOME: success` or `OUTCOME: failure` in their text output.

**Status: chat-grade / experimental** relative to `claude`/`qwen_code`/`codex_cli`/`anthropic`/`llm` — see the [capability matrix in agents.md](../agents.md#capability-matrix). Whether opencode's project-scoped config (`opencode.json`) can be used to inject the same `mcp-server` sidecar used by the other CLI providers (closing the MCP-tools and command-policy gaps below) is an open question that has not yet been investigated; until it is, treat this provider as second-class for task-editor integration purposes even though its own repo-editing toolset is otherwise capable.

## Provider String

```
"provider": "opencode"
```

## How It Works

Runs: `opencode run --format json [-m <model>] -- <prompt>`

Output is parsed as NDJSON. The `--` separator prevents the prompt content from being interpreted as CLI flags.

## Credentials

Opencode manages its own auth and model configuration. You must have the `opencode` binary installed and configured on the host (or in the container). Refer to [opencode's documentation](https://opencode.ai/docs) for setup.

**Subprocess environment:** the `opencode` subprocess does not inherit the
backend's full environment (see [providers/README.md § Subprocess
environment](README.md#subprocess-environment)). It receives `PATH`/`HOME`
(opencode's own auth store lives under `$HOME`/XDG dirs, which are also
allowlisted) plus `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`,
`GOOGLE_API_KEY` if set on the backend, a handful of locale/proxy/TLS vars,
and this agent config's own `env` field. Any other backend env var will not
reach the CLI.

## MCP Tools

**Not supported.** Opencode has no `--mcp-config` flag. The MCP sidecar is not launched for this provider.

This means:
- `update_task_notes` — **unavailable**
- `store_info` — **unavailable**
- `signal_complete` (MCP version) — **unavailable**
- `request_human` (MCP version) — **unavailable**

**Workaround for completion signalling:** The agent must output `OUTCOME: success` or `OUTCOME: failure` somewhere in its final text response. The runner scans the output for this marker.

```
OUTCOME: success
```

or

```
OUTCOME: failure
```

If neither marker is found, the run completes with status `completed` but no outcome, so no label transition occurs.

## Model Selection

Pass `model` on the referenced [Provider Config](../agents.md#provider-configs). It is passed via `-m <model>` to the CLI. If empty, opencode uses its configured default.

To see available models: `opencode models` (the UI calls `GET /api/v1/agents/models?provider=opencode` which runs this command).

## Image Attachments

**Not wired up.** `opencode run` has an `-f`/`--file` flag for attaching files to the
message (verified against v1.18.6), but task attachments are not passed to it.

## Max Turns

The `max_turns` agent config field is accepted but **not currently enforced** by this provider — the opencode CLI's `run` command has no documented turn-limit flag equivalent to `claude`'s `--max-turns`. The field is stored and available for a future opencode CLI feature, but has no effect today.

## Command Allowlist / Denylist

**Not enforced.** The `command_allowlist`/`command_denylist` agent config fields have
no effect for this provider — opencode has no Bash tool wired up server-side (it
manages its own tool permissions via its own global config, outside task-editor's
control). If you need to restrict shell command execution for an agent config, use
the `claude`, `qwen_code`, `anthropic`, or `llm` providers instead.

## Session Resume

Every event on `opencode run --format json`'s NDJSON output carries a top-level `sessionID` field (verified against opencode-ai v1.18.6), which the runner records. When a later run on the same task hits this same agent config (and the config's `resume_sessions` flag is on — the default), the runner invokes the CLI with `--session <sessionID>`, so prior context carries forward instead of starting cold.

## Cost & Usage Reporting

**Recorded.** `input_tokens`, `output_tokens`, and `cost_usd` are read directly
from the CLI's `step_finish` event: opencode's `step-finish` part carries both
a `cost` number and a `tokens` object (`{input, output, reasoning, cache:
{read, write}}`). `classifyOpencodeJSON` reads `cost` and `tokens.input`/
`tokens.output` off that event (reasoning/cache fields are not currently
tracked). Cost is authoritative — reported directly by the CLI, not estimated
via a pricing table, the same as `claude`/`qwen_code`.

`step_finish` fires once per *step*, not once per run, so a single run may
emit several of these events. opencode's own SQLite `session` table stores a
single cumulative `cost`/`tokens_input`/`tokens_output` row per session (not a
running delta per step), which strongly suggests the values on each
`step_finish` event are themselves cumulative-to-date. Based on that
evidence, the runner **takes the last `step_finish`'s usage** rather than
summing across steps. This assumption has not been independently verified
against a real multi-step authenticated run — if it's ever confirmed to be
per-step deltas instead, the runner should sum them instead of assigning.

Usage is persisted on every run outcome, including failed/timed-out runs —
money may have been spent on a run before it crashed.

_Verified against `opencode-ai` v1.18.6._ See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

**Mid-run cost kill switch: not supported.** No mid-run watchdog is wired
into this provider (unlike `claude`/`qwen_code`'s incremental token-usage
watchdog) — `step_finish`'s cumulative-to-date snapshot is not the per-turn
incremental usage a watchdog needs to project a running total mid-run. Usage
is only known at end-of-run. Only the pre-dispatch `max_cost_usd` guard
applies, which is now effective since `cost_usd` is recorded. See
[agents.md § Cost Budgets](../agents.md#cost-budgets).

## Limitations

| Feature | Status |
|---|---|
| MCP tools | ❌ Not available |
| `update_task_notes` | ❌ Not available |
| `store_info` | ❌ Not available |
| `request_human` | ❌ Not available |
| Image attachments | ❌ Not wired up — `opencode run` has an `-f`/`--file` flag, but attachments are not passed to it |
| Outcome signalling | ⚠️ Text-based only (`OUTCOME: success/failure`) |
| Rate limit detection | ✅ Implemented — stderr, and stdout lines that aren't structured JSON events, are scanned for 429 / rate-limit signals and surfaced as `ErrRateLimit`. opencode has no typed error classification, so a rate limit reported only inside a structured event body's text isn't caught on stdout (it still surfaces via stderr or a non-zero exit) — see issue #335 |
| Cost & usage reporting | ✅ Recorded from the CLI's `step_finish` event (cost + tokens), authoritative — see above |
| Command allowlist/denylist | ❌ Not enforced |
| Session resume | ✅ `sessionID` + `--session` (see above) |

## Setup Checklist

1. Install `opencode` and add it to `PATH` (or mount it into the container)
2. Configure opencode's model and auth via its own config
3. Create a [Provider Config](../agents.md#provider-configs) with `"provider": "opencode"`, then an agent config referencing it via `provider_config_id`
4. Instruct the agent in its system prompt to output `OUTCOME: success` or `OUTCOME: failure` at the end
