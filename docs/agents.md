# Agents

## Agent Configs

An agent config connects a set of workflow labels to a specific AI provider. The dispatcher matches tasks to configs by label name.

### Fields

| Field | Description |
|---|---|
| `name` | Human-readable name |
| `provider` | Provider string — see [Providers](#providers) below |
| `model` | Model identifier (e.g. `claude-sonnet-4-6`, `gpt-4o`) |
| `labels` | JSON array of label names this agent handles (e.g. `["plan","work"]`) |
| `system_prompt` | Custom system instructions; appended with MCP tool guidance automatically |
| `max_tokens` | Maximum tokens per response (0 = provider default) |
| `timeout_secs` | Maximum run duration in seconds (0 = 600s default) |
| `max_turns` | Maximum agent turns/tool-call iterations per run (0 = 50 default) |
| `max_retries` | Number of automatic consecutive retries allowed for a task after a **transient** provider error (rate limit, network blip, upstream 5xx) before it's left `failed`/escalated to `waiting_human`. `0` disables auto-retry. Default `3`. See [Retry Policy](#retry-policy) below. |
| `retry_backoff_secs` | Base backoff, in seconds, before a transient-error retry becomes eligible for re-dispatch. Exponential backoff (`base * 2^attempt`, capped at 10 minutes) is applied on top. Default `30`. |
| `env` | JSON object of additional environment variables for the agent process |
| `enabled_plugins` | JSON array of Claude plugin IDs (`"<name>@<marketplace>"`) enabled for this config. **`claude` provider only.** Defaults to `[]` (all off). See [Claude Plugins & MCP Servers](#claude-plugins--mcp-servers) below. |
| `enabled_mcp_servers` | JSON array of Claude user-level MCP server names enabled for this config. **`claude` provider only.** Defaults to `[]` (all off). See [Claude Plugins & MCP Servers](#claude-plugins--mcp-servers) below. |
| `command_allowlist` | JSON array of shell-command glob patterns (`"*"` wildcard). If non-empty, only commands matching at least one pattern may run via `run_bash`/`Bash`. Defaults to `[]` (no restriction). **Not enforced for `opencode`.** See [Command Allowlist / Denylist](#command-allowlist--denylist) below. |
| `command_denylist` | JSON array of shell-command glob patterns (`"*"` wildcard). Commands matching any pattern here are always denied, checked before `command_allowlist`. Defaults to `[]` (no restriction). **Not enforced for `opencode`; not enforced for `qwen_code`.** See [Command Allowlist / Denylist](#command-allowlist--denylist) below. |
| `resume_sessions` | Whether new runs for a task resume the previous run's provider session instead of starting cold. **`claude` provider only** (others ignore it). Default on. See [Session Resume](#session-resume) below. |
| `subtasks_enabled` | Whether this config's runs may decompose their task into subtasks via the `create_subtask` MCP tool. **`claude`/`qwen_code` only.** Off by default — grant it to a specific agent (typically the planner). See [Subtasks](workflows.md#subtasks-agent-driven-decomposition). |
| `max_subtasks` | Per-parent cap on children a run may create. Default 10. |

## Providers

| Provider string | Description | MCP Tools | Details |
|---|---|---|---|
| `claude` | Claude CLI subprocess (`claude -p ...`) | ✅ All 5 | [providers/claude.md](providers/claude.md) |
| `anthropic` | Anthropic Messages API (direct HTTP) | ❌ Native tools | [providers/anthropic.md](providers/anthropic.md) |
| `opencode` | Opencode CLI (`opencode run --format json`) | ❌ None | [providers/opencode.md](providers/opencode.md) |
| `qwen_code` | Qwen Code CLI (`qwen -p ...`) | ✅ All 5 | [providers/qwen_code.md](providers/qwen_code.md) |
| _(any other value)_ | OpenAI-compatible API at `LLM_BASE_URL` | ❌ Native tools | [providers/llm.md](providers/llm.md) |

For per-provider deep-dives (credentials, tool availability, limitations, setup), see the [providers/](providers/) directory.

## Dispatcher

The dispatcher runs a background goroutine that sweeps the database every 5 seconds:

1. Queries `ListAgentPickupTasks` — tasks whose label appears in any agent-triggerable transition AND whose `active_agent_run_id IS NULL`.
2. Loads all agent configs, matches each task to the first config whose `labels` array contains the task's label.
3. Creates an `agent_runs` record with status `pending`.
4. Sets the task's `active_agent_run_id` (and updates `current_agent_run_id`) — this prevents the next sweep from double-dispatching.
5. Submits a `Job` to the worker pool. If the pool queue is full, marks the run `failed` and clears `active_agent_run_id`.

## Worker Pool

The pool manages `MAX_WORKERS` concurrent goroutines (default 5). Each worker:

1. Sets the run status to `running`.
2. Calls `Provider.Run()` which streams `LogEntry` values on a channel.
3. Persists log entries to `agent_logs` in batched transactions (flush every 500ms or every 50 entries).
4. Simultaneously publishes each entry to the WebSocket hub for live streaming.
5. On completion, sets the run status to `completed`, `failed`, or `waiting_human`.
6. For `completed`/`failed`, clears `active_agent_run_id` so the dispatcher can re-pick-up the task.
7. For `waiting_human`, leaves `active_agent_run_id` set — dispatch is blocked until a human approves or rejects.

## Run Lifecycle

```
created (pending)
    │
    ▼ worker picks up
running
    │
    ├─▶ completed  → engine.Transition(nextLabel)  → active_run cleared
    ├─▶ failed                                      → active_run cleared
    └─▶ waiting_human → task.needs_human WS event  → active_run stays set
                            │
                    human approves/rejects
                            │
                    engine.Transition()              → active_run cleared
```

## Log Entry Types

| Type | Meaning |
|---|---|
| `system` | Internal messages (process start, timeout, exit) |
| `stdout` | Agent text output / parsed assistant messages |
| `stderr` | Stderr from the subprocess |
| `tool_call` | Tool invocation (Edit, Bash, signal_complete, etc.) |
| `tool_result` | Result returned to the agent after a tool call |

## Cost & Usage Tracking

Each `agent_runs` row records `input_tokens`, `output_tokens`, and `cost_usd` for the run, captured differently per provider:

| Provider | Usage source | Notes |
|---|---|---|
| `claude` | CLI's own `result` stream-json message (`usage` + `total_cost_usd`) | Authoritative — the CLI itself knows whether you're on a Claude Max subscription (often `$0`) or metered API billing, so `cost_usd` is used as-is, not estimated. |
| `qwen_code` | Same `result` envelope parsing as `claude` (`classifyStreamJSON`) | Same authoritative behavior as `claude`, assuming the qwen CLI's stream-json output stays compatible. |
| `anthropic` | Messages API `usage` field, summed across every turn of the agentic loop | `cost_usd` is *estimated* by multiplying tokens by a small, manually maintained USD-per-1M-token pricing table (`internal/agent/pricing.go`). Unknown models fall back to $0 rather than a guessed price. |
| `llm` | OpenAI-compatible `usage` field (`prompt_tokens`/`completion_tokens`), summed across every turn | Same estimation approach and pricing table as `anthropic`. |
| `opencode` | Not currently exposed in `opencode run --format json` output | Usage/cost is left at `0` — not estimated — until opencode's JSON schema includes a usage field. |

The pricing table is intentionally approximate and small (a hardcoded Go map); it will drift from live pricing over time and is not currently user-editable.

The Dashboard shows an aggregate total (tokens + cost) across all runs in a terminal state (`completed`/`failed`/`waiting_human`), plus a per-provider breakdown (via `agent_configs.provider`, joined on `agent_runs.agent_config_id`). The aggregate total query does not join on `agent_configs`, so it includes every terminal run regardless of its config. The per-provider breakdown *does* join on `agent_configs`, so runs whose agent config was later deleted (`agent_config_id` is set `NULL` on delete) are excluded from that breakdown, since they can no longer be attributed to a provider — a known limitation.

## Session Resume

Each `claude`/`qwen_code` run's stream-json output carries a `session_id`; the
pool persists it on the run row. When the dispatcher starts a new run for a
task whose **same agent config** previously recorded a session — a feedback
loop back to `work`, a re-run after a genuine failure, a reply to
`request_human` — and the config's `resume_sessions` is on, the `claude`
provider is invoked with `--resume <session_id>` so the new run continues the
same conversation with full prior context, instead of re-deriving it from the
repo. Currently `claude`-only; `qwen_code` records its session but is not
resumed (no verified CLI flag semantics yet).

Behavior details:

- **Condensed prompt on resume.** A resumed conversation already contains the
  task title/description/notes as its own turns, so only the *new* information
  is sent as the next message: the human reply, rejection feedback, and open
  review comments (plus a short continuation instruction).
- **Cold-start fallback.** If the CLI reports the session can't be found — or
  exits with an error before producing any stream output — the runner retries
  once without `--resume`, using the full prompt. Resume failures are never
  fatal.
- **System prompt still applies.** The CLI rebuilds the system prompt from
  flags on every invocation (sessions persist the transcript, not the system
  prompt), so the agent config's `system_prompt` is re-applied on resume
  exactly as on a cold start. Note that prior conversation turns still exert
  behavioral pull — an agent that spent many turns building something keeps
  thinking like the builder — which is why `resume_sessions` is per-config:
  stages that should look at the work with **fresh eyes** (e.g. an
  agent-review stage) should turn it off.
- The `"NOTES FROM PRIOR AGENT"` handoff is unchanged and still the mechanism
  for context transfer **between different agent configs** — resume only ever
  applies within the same config.

## Replying to `request_human`

When a run pauses on `waiting_human` (the agent called `request_human`), a
human can now **answer with text** instead of only approving/rejecting:
`POST /tasks/{id}/runs/{run_id}/reply` (or the reply box on the task detail
page) starts a continuation run carrying the reply. With session resume, the
reply lands as the next message of the same conversation; without it, the run
starts cold with the reply at the top of the prompt under
`RESPONSE FROM HUMAN`. The task stays on its label, and the replied-to run
keeps its `waiting_human` status. See [api.md](api.md) for status codes.

## Prompt Construction

The user prompt sent to the agent is assembled as:

```
[FEEDBACK FROM PRIOR REVIEW:
<feedback from the previous run, if any>

---]

[NOTES FROM PRIOR AGENT:
<agent_notes from the task, if any>

---]

Task: <title>

<description>

[ATTACHED IMAGES (available in .task_attachments/ within the repo):
- .task_attachments/<filename>
...]
```

The `"NOTES FROM PRIOR AGENT:"` section contains the task's `agent_notes` field — content that prior agents wrote via `update_task_notes`. This is the primary handoff mechanism between agents in a multi-step workflow.

### System Prompt Construction

The system prompt is the agent config's `system_prompt` field (defaults to a generic software engineer instruction if empty), with the following always appended:

1. **Repo path injection:** `"The repository you are working on is located at: <path>"`
2. **Notes handling instruction:** tells the agent to read the NOTES FROM PRIOR AGENT section carefully
3. **Notes writing instruction:** tells the agent to call `update_task_notes` before `signal_complete`, using `append:true` if prior notes were present
4. **Completion instruction:** `"When your work is complete, call the mcp__task-editor__signal_complete tool with outcome='success' if the work succeeded or outcome='failure' if it did not. If the MCP tool is unavailable, end your final response with exactly: OUTCOME: success  or  OUTCOME: failure"`

## MCP Tools (claude and qwen_code providers)

When `MCP_SERVER_PATH` is set, the `claude` and `qwen_code` providers launch an MCP sidecar that exposes 5 tools:

| Tool | Description |
|---|---|
| `get_task_transitions` | Call first — returns available transitions for the current label |
| `signal_complete(outcome, summary)` | Mark the run done; outcome must be `"success"` or `"failure"` |
| `request_human(message)` | Pause for human input |
| `update_task_notes(notes, append?)` | Write notes for subsequent agents |
| `store_info(info)` | Store run summary visible in the task UI |

See [mcp-tools.md](mcp-tools.md) for the full tool reference including parameters, return values, and behaviour details.

## Claude Plugins & MCP Servers

For the `claude` provider only, each agent config can select which Claude Code plugins and user-level MCP servers are enabled for its runs. **Everything defaults to off** — nothing is enabled unless explicitly selected.

- **Discovery source:** options are read dynamically from the server's own Claude home directory, not hardcoded:
  - Plugins: `~/.claude/plugins/installed_plugins.json` (the `plugins` object's keys, `"<name>@<marketplace>"`).
  - MCP servers: the **global/user-level** `mcpServers` key in `~/.claude.json`. Project-scoped servers under `projects["<path>"].mcpServers` are **not** included — only servers configured at the user level.
- **API:** `GET /agents/claude-options` returns the currently discovered `{ plugins: [{id, name, marketplace}], mcp_servers: [name] }` for the frontend to render as selection chips.
- **Enforcement at run time (`claude.go`):**
  - Plugins: the `claude` CLI is invoked with `--settings '{"enabledPlugins": {...}}'`, built by defaulting every discovered plugin to `false` and then setting `true` only for IDs present in `enabled_plugins`. A plugin selected but not present in the current discovery snapshot is still explicitly enabled (stale-inventory fallback).
  - MCP servers: for each name in `enabled_mcp_servers` (skipping the reserved `task-editor` name), its raw config entry is read from `~/.claude.json`'s global `mcpServers` map and merged into the `--mcp-config` file alongside the task-editor sidecar entry. A bare `mcp__<server>` entry is appended to `--allowedTools` per selected server so its tools aren't blocked — this wildcarding behavior is inferred from CLI docs and worth re-verifying against a live run if MCP tool calls are unexpectedly denied.
- **Scope:** this is currently `claude`-provider-only. Other providers (`anthropic`, `opencode`, `qwen_code`, generic `llm`) have the same DB columns available but ignore them entirely.

## Command Allowlist / Denylist

Every agent config has an optional `command_allowlist` and `command_denylist` — JSON
arrays of shell-command glob patterns (`"*"` wildcard), both defaulting to `[]` (no
restriction, i.e. today's existing behavior for all pre-existing configs). These let
you limit which shell commands a given agent config's Bash/`run_bash` tool may run,
reducing the blast radius of a misbehaving or prompt-injected agent — **this is
best-effort string matching, not a sandbox.** It does not prevent an agent from
constructing a denied command indirectly (via `$()`, backticks, string
concatenation, base64-decoded payloads, etc).

- **Denylist always wins:** if a command matches any `command_denylist` pattern, it
  is refused regardless of `command_allowlist`.
- **Allowlist, if non-empty, is exclusive:** a command must match at least one
  `command_allowlist` pattern to run. An empty allowlist means "no allowlist
  restriction" (any command not denylisted may run).
- **Pattern syntax:** `*` matches any sequence of characters (including empty).
  Matching is case-sensitive and applied to the whole, trimmed command string (e.g.
  `"git *"` matches `"git status"` but not `"echo git status"`; `"* --force"`
  matches any command ending in `--force`).

**Enforcement differs by provider:**

| Provider | Enforcement |
|---|---|
| `anthropic`, `llm` | Enforced in Go, in `executeLLMTool`, immediately before spawning the `run_bash` subprocess. Both allowlist and denylist fully supported. |
| `claude` | Enforced natively by the `claude` CLI via `permissions.allow`/`permissions.deny` in the `--settings` JSON (same `Bash(pattern)` syntax as `--allowedTools`/`--disallowedTools`). Both allowlist and denylist supported; smoke-tested against a live `claude` binary. |
| `qwen_code` | `command_allowlist` is enforced natively via `--allowed-tools Bash(pattern)` entries. `command_denylist` is **not enforced** — no confirmed `qwen` CLI denylist flag exists. |
| `opencode` | **Not enforced at all** — opencode has no Bash tool wired up server-side; it manages tool permissions via its own global config. |

See the corresponding [provider docs](#providers) for details on each provider's mechanism.

## Retry Policy

Not every `failed` run means the agent's work actually failed — sometimes
it's a transient infrastructure hiccup (an API rate limit, a network blip, an
upstream `5xx`, an ambiguous timeout). The retry policy distinguishes the two
so humans aren't paged for problems that will resolve themselves:

- **Genuine failure** — the agent ran and the work itself failed (e.g. tests
  didn't pass, the agent gave up). Behaves as before: the task stays on its
  current label and the next dispatcher sweep (~5s) re-picks it up
  immediately, with no retry limit.
- **Transient failure** — a rate limit (`429`), a network-level error,
  upstream `5xx`, or (for CLI providers) a best-effort text match on
  stdout/stderr for signals like connection resets or `502/503/504`. These
  are auto-retried up to `max_retries` times with exponential backoff
  (`retry_backoff_secs * 2^attempt`, capped at 10 minutes) before the task is
  escalated to `waiting_human` — so a human only gets involved once the
  automatic retries have been exhausted, not on every blip.
- The task board shows a **"↻ Retrying (n)"** badge while a task is in a
  backed-off auto-retry window, so it's clear at a glance that no action is
  needed yet.
- Setting `max_retries` to `0` disables auto-retry for that config entirely,
  reverting to the old behavior (immediate, unbounded re-dispatch) for
  transient errors too.
- This is separate from, and complementary to, the existing per-agent-config
  rate-limit block: a `429` still blocks the *whole config* from new
  dispatches for a backoff window (protects shared credentials/quota) **and**
  counts against the *specific task's* retry budget.

## Environment Variable Security

The `env` field in agent configs passes additional env vars to the subprocess. Keys that could hijack process execution are blocked and logged as warnings:

`PATH`, `LD_PRELOAD`, `LD_LIBRARY_PATH`, `HOME`, `SHELL`, `IFS`, `DYLD_INSERT_LIBRARIES`, `DYLD_LIBRARY_PATH`
