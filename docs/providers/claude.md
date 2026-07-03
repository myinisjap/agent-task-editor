# Provider: `claude`

The `claude` provider runs the Claude CLI in headless mode and is the most feature-rich option — it supports MCP tools, image attachments, and uses the `~/.claude` OAuth credentials so a Claude Max subscription covers all costs.

## Provider String

```
"provider": "claude"
```

## How It Works

Runs: `claude -p <prompt> --system-prompt <system> --output-format stream-json --verbose --allowedTools <tools> --max-turns <max_turns> --bare`

`<max_turns>` comes from the agent config's `max_turns` field (defaults to `50` when unset or `0`).

Output is parsed as NDJSON (stream-json format). The MCP sidecar is launched as a subprocess alongside `claude` and connected via `--mcp-config <tempfile>`.

## Credentials

- **Claude Max subscription** (recommended) — authenticate once with `claude login` on the host; credentials live in `~/.claude/.credentials.json`
- **Or Anthropic API key** — set `ANTHROPIC_API_KEY` in the environment; Claude CLI will use it directly

When using Docker, mount the `~/.claude` directory into the container so the credentials are available:

```yaml
volumes:
  - ~/.claude:/root/.claude
  - /usr/local/bin/claude:/usr/local/bin/claude:ro
```

The `ANTHROPIC_AUTH_TOKEN` is auto-injected from `~/.claude/.credentials.json` at run time.

## MCP Tools

**All 5 MCP tools are supported** when `MCP_SERVER_PATH` is set.

| Tool | Description |
|---|---|
| `mcp__task-editor__get_task_transitions` | Returns available workflow transitions |
| `mcp__task-editor__signal_complete` | Marks the run done with `success` or `failure` |
| `mcp__task-editor__request_human` | Pauses the run for human input |
| `mcp__task-editor__update_task_notes` | Writes persistent notes for subsequent agents |
| `mcp__task-editor__store_info` | Stores a summary visible in the task UI |

Without `MCP_SERVER_PATH`, these tools are unavailable and the run completes with status `completed` (no label transition is attempted).

See [mcp-tools.md](../mcp-tools.md) for full tool reference.

## Allowed Tools (passed via `--allowedTools`)

```
Edit, Write, Read, Bash, Glob, Grep
mcp__task-editor__get_task_transitions
mcp__task-editor__signal_complete
mcp__task-editor__request_human
mcp__task-editor__update_task_notes
mcp__task-editor__store_info
```

For each MCP server name in the agent config's `enabled_mcp_servers`, a bare `mcp__<server>` entry is also appended so that server's tools aren't blocked by the allowlist (server-level wildcarding, inferred from CLI docs — worth re-verifying against a live run if tool calls from a selected MCP server are unexpectedly denied).

## Plugins & User MCP Servers (per-agent-config selection)

Each `claude`-provider agent config can select which locally-installed Claude plugins and user-level MCP servers are enabled, via the `enabled_plugins` and `enabled_mcp_servers` fields. **Both default to empty (all off).** Options are discovered dynamically — not hardcoded — from:

- `~/.claude/plugins/installed_plugins.json` for plugins
- the global `mcpServers` key in `~/.claude.json` for MCP servers (project-scoped servers are not included)

`GET /agents/claude-options` exposes the current discovery snapshot for the frontend to render as selectable options.

At run time:

- **Plugins** are enabled/disabled via `--settings '{"enabledPlugins": {...}}'`. This replaces the previous hardcoded `--settings '{"enabledPlugins":{"oh-my-claudecode@omc":false}}'` — the settings payload is now built dynamically: every plugin discovered on the machine defaults to `false`, and only IDs present in `enabled_plugins` are set to `true`.
- **MCP servers** selected in `enabled_mcp_servers` have their raw config entries copied from `~/.claude.json`'s global `mcpServers` map into the same `--mcp-config` temp file used for the task-editor sidecar (an MCP config file is generated even if the task-editor sidecar itself is disabled, as long as at least one user MCP server is selected). The reserved name `task-editor` is always skipped to avoid colliding with the sidecar entry.

See [agents.md § Claude Plugins & MCP Servers](../agents.md#claude-plugins--mcp-servers) for more detail. This feature is `claude`-provider-only for now.

## Command Allowlist / Denylist

`command_allowlist` and `command_denylist` (both JSON arrays of `"*"`-wildcard glob
patterns, defaulting to `[]`/no restriction) are translated into Claude Code's native
`permissions.allow` / `permissions.deny` settings keys, passed via the same
`--settings` JSON blob used for `enabledPlugins`. Each pattern is wrapped as
`Bash(pattern)` — identical to the syntax Claude Code's `--allowedTools` /
`--disallowedTools` flags accept.

**`command_denylist` is fully enforced and reliable.** This was smoke-tested against a
live `claude` binary (v2.1.198): a `permissions.deny` entry matching a requested Bash
command caused the CLI to refuse the tool call (`permission_denials` in the result
JSON), both via the `--settings` JSON route and the equivalent `--disallowedTools`
flag. Denylist is checked by the Claude CLI before any allow entries and always wins.

**`command_allowlist` is *not* an effective restriction for this provider — known
gap.** Live testing showed that `permissions.allow` / `--allowedTools` entries for
`Bash(pattern)` only *auto-approve* matching commands (skip the confirmation
prompt); they do not make Bash default-deny for non-matching commands. Because the
bare `Bash` tool is already granted (required so the agent can run any command at
all), a command that matches no `command_allowlist` pattern is still executed — it is
simply not silently auto-approved. This was verified with both the `--settings`
JSON `permissions.allow` route and the `--allowedTools "Bash(pattern)"` flag route,
with and without bare `Bash` present in `--allowedTools`; in every case a
non-matching command still ran without any permission denial. There is currently no
known `claude` CLI mechanism to make Bash itself default-deny while allowing only
specific patterns (short of `--disallowedTools "Bash"` plus enumerating every
allowed exact command via denylist-of-everything-else, which isn't practical for
glob-style allowlisting). **If you need to restrict which commands an agent can run,
rely on `command_denylist` for the `claude` provider; `command_allowlist` is
currently a no-op here** (the field/UI still exists for forward-compatibility and
for parity with the `anthropic`/`llm` providers, where it is fully enforced).

Denylist enforcement here is done by the Claude CLI itself (not task-editor's own
process), so it is somewhat more robust than the generic `run_bash` string-match
enforcement used by the `anthropic`/`llm` providers — but it is still glob-pattern
matching on the command string, not a sandbox.

## Image Attachments

Supported. Files uploaded to a task are passed via `--image <path>` flags. The server resolves absolute paths from the `UPLOAD_DIR`.

## Model Selection

Pass `model` in the agent config (e.g. `claude-sonnet-4-6`, `claude-opus-4`). If empty, the Claude CLI uses its own default.

## Cost & Usage Reporting

Token usage and cost are parsed from the CLI's `result` stream-json message (`usage` + `total_cost_usd`) and are **authoritative** — the CLI itself knows whether it's running under a Claude Max subscription (often `$0`) or metered API billing, so no estimation is applied. See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

## Rate Limit Handling

The runner detects 429 responses in stdout/stderr (looks for `429`, `Request rejected`, `rate limit`) and returns `ErrRateLimit`. The dispatcher will back off and retry.

## Environment Variable Security

The `env` field in agent configs passes extra vars to the subprocess. Dangerous keys (`PATH`, `LD_PRELOAD`, `HOME`, `SHELL`, `IFS`, `DYLD_INSERT_LIBRARIES`, `DYLD_LIBRARY_PATH`) are blocked and logged as warnings.

## Setup Checklist

1. Install the `claude` CLI: `npm install -g @anthropic-ai/claude-code` (or equivalent)
2. Authenticate: `claude login`
3. Set `MCP_SERVER_PATH` to the path of the built `mcp-server` binary
4. Mount `~/.claude` and the `claude` binary into Docker (if using Docker)
5. Create an agent config with `"provider": "claude"`
