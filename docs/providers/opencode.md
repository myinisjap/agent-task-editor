# Provider: `opencode`

The `opencode` provider runs the [opencode](https://opencode.ai) CLI. **MCP tools are not supported** — opencode has no `--mcp-config` flag. Agents must signal completion by printing `OUTCOME: success` or `OUTCOME: failure` in their text output.

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

Pass `model` in the agent config. It is passed via `-m <model>` to the CLI. If empty, opencode uses its configured default.

To see available models: `opencode models` (the UI calls `GET /api/v1/agents/models?provider=opencode` which runs this command).

## Image Attachments

Not supported.

## Max Turns

The `max_turns` agent config field is accepted but **not currently enforced** by this provider — the opencode CLI's `run` command has no documented turn-limit flag equivalent to `claude`'s `--max-turns`. The field is stored and available for a future opencode CLI feature, but has no effect today.

## Command Allowlist / Denylist

**Not enforced.** The `command_allowlist`/`command_denylist` agent config fields have
no effect for this provider — opencode has no Bash tool wired up server-side (it
manages its own tool permissions via its own global config, outside task-editor's
control). If you need to restrict shell command execution for an agent config, use
the `claude`, `qwen_code`, `anthropic`, or `llm` providers instead.

## Limitations

| Feature | Status |
|---|---|
| MCP tools | ❌ Not available |
| `update_task_notes` | ❌ Not available |
| `store_info` | ❌ Not available |
| `request_human` | ❌ Not available |
| Image attachments | ❌ Not supported |
| Outcome signalling | ⚠️ Text-based only (`OUTCOME: success/failure`) |
| Rate limit detection | ❌ Not implemented |
| Command allowlist/denylist | ❌ Not enforced |

## Setup Checklist

1. Install `opencode` and add it to `PATH` (or mount it into the container)
2. Configure opencode's model and auth via its own config
3. Create an agent config with `"provider": "opencode"`
4. Instruct the agent in its system prompt to output `OUTCOME: success` or `OUTCOME: failure` at the end
