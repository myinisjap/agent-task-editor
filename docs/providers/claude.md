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

## Session Resume

Every stream-json envelope carries the conversation's `session_id`; the runner
extracts it and the pool persists it on the run row. When the same agent
config runs the task again (and the config's `resume_sessions` flag is on —
the default), the CLI is invoked with `--resume <session_id>` and a
**condensed prompt** containing only the new information (human reply,
rejection feedback, open review comments) — the resumed conversation already
contains the task context as its own turns.

- **Fallback:** if the CLI prints a session-not-found error (best-effort text
  match: "No conversation found …"), or exits with an error before producing
  any stream output, the runner retries once cold with the full prompt.
- **System prompt:** rebuilt from `--system-prompt` on every invocation —
  sessions persist the transcript, not the system prompt — so the config's
  system prompt applies on resume too. Worth re-verifying against a live CLI
  run if resume behavior looks off after a CLI upgrade (same spirit as the
  `--allowedTools` wildcarding note below).

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

### Automatic OAuth token refresh

OAuth access tokens expire after a few hours. Before injecting the token
(and before the dashboard usage widget calls Anthropic), the server checks
the credentials file's `expiresAt`: if the token is expired or expires
within 5 minutes, it is refreshed against Anthropic's OAuth token endpoint
(`https://console.anthropic.com/v1/oauth/token`, using Claude Code's public
client ID) with the stored `refreshToken`, and the rotated
access/refresh tokens are written back to `~/.claude/.credentials.json`
atomically (0600 perms, all other fields preserved) so this app and Claude
Code on the same machine stay in sync. Refreshes are serialized behind a
process-wide mutex because Anthropic rotates the refresh token on use.

Previously the raw `accessToken` was injected as-is, so once it expired
every run got 401s until you ran Claude Code interactively on the host to
refresh the file. That manual step is no longer needed. Failure modes:

- **Proactive refresh fails but the token is still valid** → the current
  token is used.
- **Token expired and refresh fails** (e.g. refresh token revoked) →
  nothing is injected; the `claude` CLI falls back to its own refresh flow
  against the credentials file. If that fails too, a fresh `claude login`
  is required — the run escalates to `waiting_human` via the `auth`
  classification as before.

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

The runner detects 429 responses in stdout/stderr (looks for `429`, `Request rejected`, `rate limit`, `session limit`, `usage limit`) and returns `ErrRateLimit`. The dispatcher will back off and retry.

For the CLI's structured `stream-json` terminal `"result"` event, the runner also
checks the `api_error_status` field directly (treating `429` as an unconditional
rate limit regardless of wording) and parses Anthropic's session/usage-limit
message text for an exact reset time, e.g.:

```
"You've hit your session limit · resets 6pm (America/Chicago)"
```

`parseClaudeResetTime` (in `claude_reset.go`) extracts the clock time and IANA
zone, resolves it to the next occurrence of that wall-clock time (today or
tomorrow, whichever is later than the current time), and adds a 1-minute
buffer — the task is then rescheduled to retry ~1 minute after the limit
actually resets, instead of always falling back to generic exponential
backoff. If no reset clue can be parsed, `ErrRateLimit.ResetAt` is left zero
and the pool falls back to `BlockWithBackoff` as before.

`claude_reset.go` blank-imports `time/tzdata` to embed the IANA time zone
database into the compiled binary — the production container
(`node:26-alpine`) does not ship `/usr/share/zoneinfo`, so without this
`time.LoadLocation("America/Chicago")` would fail there and reset-time
parsing would silently degrade to backoff-only in production while working
fine in local dev.

## Dashboard: Live Claude Usage Widget

The Dashboard page shows a "Claude usage" widget with the current account's
rate-limit utilization for the rolling 5-hour window and the weekly (7-day)
window, plus each window's reset time — the same numbers `claude`/the OMC
HUD plugin show. This is fetched live from Anthropic's OAuth usage endpoint
(`GET https://api.anthropic.com/api/oauth/usage`) using the OAuth access
token from `~/.claude/.credentials.json` (i.e. it requires a Claude Max/Pro
account authenticated via `claude login` — a bare `ANTHROPIC_API_KEY`, or
the `anthropic`/`llm` providers, do not produce this file).

It is unrelated to the "Cost & usage" section further down the Dashboard,
which aggregates token/cost totals from this app's own `agent_runs` table.

The widget degrades silently: if `~/.claude/.credentials.json` is missing
(no `claude login`, Docker deployment without `~/.claude` mounted, CI/test
environment, or an API-key-only setup) or the live fetch to Anthropic fails
for any reason, `/dashboard`'s `claude_usage.available` is `false` and the
widget is simply omitted — it never causes the Dashboard to error or hang.
The server also caches the result for ~45s to avoid calling Anthropic's
usage endpoint on every WS-triggered dashboard refresh.

## Environment Variable Security

The `env` field in agent configs passes extra vars to the subprocess. Dangerous keys (`PATH`, `LD_PRELOAD`, `HOME`, `SHELL`, `IFS`, `DYLD_INSERT_LIBRARIES`, `DYLD_LIBRARY_PATH`) are blocked and logged as warnings.

## Setup Checklist

1. Install the `claude` CLI: `npm install -g @anthropic-ai/claude-code` (or equivalent)
2. Authenticate: `claude login`
3. Set `MCP_SERVER_PATH` to the path of the built `mcp-server` binary
4. Mount `~/.claude` and the `claude` binary into Docker (if using Docker)
5. Create an agent config with `"provider": "claude"`
