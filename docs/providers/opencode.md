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

**Not recorded — but this is a parser gap, not a CLI gap.** `input_tokens`,
`output_tokens`, and `cost_usd` are all left at `0` (not estimated) for this
provider because `classifyOpencodeJSON` only reads `type`, `part.type`,
`part.text`, and `part.reason` off each NDJSON line.

The CLI does emit the data: opencode's `step-finish` part carries both a `cost`
number and a `tokens` object (`{input, output, reasoning, cache: {read, write}}`),
and `run --format json` surfaces that part as the `step_finish` event this parser
already handles. Reading those two fields into `runUsage` — the same way
`parse_streamjson.go` does for the `claude`/`qwen_code` `result` message — would
close the gap. Until then, a cost budget cap will not fire for this provider.

_Verified against `opencode-ai` v1.18.6._ See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

**Mid-run cost kill switch: not supported.** opencode records no usage at all (above), so there is nothing to project a mid-run cost from. Only the pre-dispatch `max_cost_usd` guard applies, and it too will not reliably fire since `cost_usd` is always `0` for this provider. See [agents.md § Cost Budgets](../agents.md#cost-budgets).

## Limitations

| Feature | Status |
|---|---|
| MCP tools | ❌ Not available |
| `update_task_notes` | ❌ Not available |
| `store_info` | ❌ Not available |
| `request_human` | ❌ Not available |
| Image attachments | ❌ Not wired up — `opencode run` has an `-f`/`--file` flag, but attachments are not passed to it |
| Outcome signalling | ⚠️ Text-based only (`OUTCOME: success/failure`) |
| Rate limit detection | ✅ Implemented — stdout/stderr are scanned for 429 / rate-limit signals and surfaced as `ErrRateLimit` |
| Cost & usage reporting | ❌ Not recorded (parser gap, not a CLI gap — see above) |
| Command allowlist/denylist | ❌ Not enforced |
| Session resume | ✅ `sessionID` + `--session` (see above) |

## Setup Checklist

1. Install `opencode` and add it to `PATH` (or mount it into the container)
2. Configure opencode's model and auth via its own config
3. Create a [Provider Config](../agents.md#provider-configs) with `"provider": "opencode"`, then an agent config referencing it via `provider_config_id`
4. Instruct the agent in its system prompt to output `OUTCOME: success` or `OUTCOME: failure` at the end
