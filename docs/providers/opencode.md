# Provider: `opencode`

The `opencode` provider runs the [opencode](https://opencode.ai) CLI. opencode has no `--mcp-config` flag or `--system-prompt` flag like `claude`/`qwen_code`, and no `-c` config-override flag like `codex_cli`, so all of the task-editor MCP tools, the system prompt, the `max_turns` cap, and the command allowlist/denylist are delivered together via the `OPENCODE_CONFIG_CONTENT` environment variable (supported in opencode-ai v1.19.1) — see [How It Works](#how-it-works). Agents can signal completion via the `signal_complete` MCP tool; a text `OUTCOME: success`/`OUTCOME: failure` marker is still honored as a fallback in case the MCP tool call doesn't land.

**Status: at parity** with `claude`/`qwen_code`/`codex_cli` for task-editor tools, `max_turns`, and command allowlist/denylist — see the [capability matrix in agents.md](../agents.md#capability-matrix). opencode's own MCP tool-naming convention and its `permission.bash` pattern-matching semantics (most-specific-wins vs. first-match, whether `"*"` is a wildcard default) have not been verified against a live `opencode` binary; the text `OUTCOME:` fallback is kept specifically because of the tool-naming uncertainty. User-level MCP servers/plugins (as opposed to the task-editor sidecar) and image attachments remain unsupported.

## Provider String

```
"provider": "opencode"
```

## How It Works

Runs: `opencode run --format json [-m <model>] [--session <id>] [--variant <effort>] [-f <file> ...] -- <prompt>`

Output is parsed as NDJSON. The `--` separator prevents the prompt content from being interpreted as CLI flags. The `-f <file>` arguments (one per task attachment, see [Image Attachments](#image-attachments)) are always placed *before* the `--` separator — `-f`/`--file` is a variadic (array-type) option in opencode's yargs-based CLI, so a bare `-f a.png "<prompt>"` would swallow the prompt as a second file; repeating `-f` per path and keeping `--` immediately before the prompt guards against that.

`opencode run` has no `--system-prompt`/`-p` flag, `--mcp-config` flag, turn-cap
flag, or command-policy flag, so all of the following are delivered together
via the `OPENCODE_CONFIG_CONTENT` environment variable (supported in
opencode-ai v1.19.1):

- The **system prompt** (the agent config's own `system_prompt`, plus the
  task-editor boilerplate — critically, the instruction to end with
  `OUTCOME: success`/`OUTCOME: failure`), set on the `build` agent's
  `prompt` field — the agent `opencode run` uses when none is otherwise
  specified.
- The **MCP sidecar** (`mcp.task-editor`), when `MCP_SERVER_PATH` is
  configured — see [MCP Tools](#mcp-tools).
- The **`max_turns` cap**, set on `agent.build.steps` — see
  [Max Turns](#max-turns).
- The **command allowlist/denylist**, mapped to a `permission.bash` block —
  see [Command Allowlist / Denylist](#command-allowlist--denylist).

This env var is generated per run by the runner itself; it is not part of
the subprocess environment allowlist described below, and it always
overrides any `OPENCODE_CONFIG_CONTENT` set in the agent config's own `env`
field — letting an operator-set value win would silently drop the task
tools, the turn cap, and the command policy along with the system prompt.

## Credentials

Opencode manages its own auth and model configuration. You must have the `opencode` binary installed and configured on the host (or in the container). Refer to [opencode's documentation](https://opencode.ai/docs) for setup.

The `opencode` CLI is not installed in the backend image by default — build with `INSTALL_OPENCODE_CLI=true` to have the backend `Dockerfile` `npm install -g opencode-ai` for you (see the backend `Dockerfile`'s `INSTALL_OPENCODE_CLI` build arg, pinned to `OPENCODE_CLI_VERSION`), or install it yourself with `npm i -g opencode-ai` and mount it into the container.

**Subprocess environment:** the `opencode` subprocess does not inherit the
backend's full environment (see [providers/README.md § Subprocess
environment](README.md#subprocess-environment)). It receives `PATH`/`HOME`
(opencode's own auth store lives under `$HOME`/XDG dirs, which are also
allowlisted) plus `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`,
`GOOGLE_API_KEY` if set on the backend (plus the always-injected
`OPENCODE_DISABLE_AUTOUPDATE` below), a handful of locale/proxy/TLS vars,
and this agent config's own `env` field. Any other backend env var will not
reach the CLI.

`OPENCODE_DISABLE_AUTOUPDATE=1` is automatically set in the environment (headless runs and the interactive chat terminal) so the CLI never self-updates away from the version pinned by `OPENCODE_CLI_VERSION` in the backend `Dockerfile` — mirroring `claude`'s `DISABLE_AUTOUPDATER` and `qwen_code`'s `NO_UPDATE_NOTIFIER`.

## MCP Tools

**Supported**, when `MCP_SERVER_PATH` is configured on the backend. opencode
has no `--mcp-config` flag like `claude`/`qwen_code`, so the sidecar is
instead declared inline in `OPENCODE_CONFIG_CONTENT`'s `mcp` block:

```json
"mcp": {
  "task-editor": {
    "type": "local",
    "command": ["/path/to/mcp-server"],
    "enabled": true,
    "environment": { "RUN_ID": "...", "RESULT_FILE": "...", "...": "..." }
  }
}
```

The `environment` map is the same one `MCPManager.Prepare` builds for the
file-based providers (`RUN_ID`, `RESULT_FILE`, `TRANSITIONS`,
`REVIEW_COMMENTS`, and — when subtasks are enabled —
`SUBTASKS_ENABLED`/`BACKEND_URL`/`TASK_ID`/`API_TOKEN`/`MAX_SUBTASKS`), so the
sidecar's scoped per-run bearer token and `RESULT_FILE` handoff work exactly
as documented in ADR 0004. This means:

- `get_task_transitions` — **available**
- `signal_complete` — **available** (MCP tool call)
- `request_human` — **available**
- `update_task_notes` — **available**
- `store_info` — **available**
- `resolve_comment` — **available**
- `create_subtask` — **available** when the agent config has subtasks enabled

opencode's own tool-naming convention for MCP tools (e.g. whether it exposes
`mcp__task-editor__signal_complete` verbatim, like `claude`/`qwen_code`, or
something else) has **not been verified live** against the `opencode`
binary. `buildSystemPrompt`'s `mcp__task-editor__*` names are passed through
unchanged.

**Fallback completion signalling:** because of the tool-naming uncertainty
above, the agent can still output `OUTCOME: success` or `OUTCOME: failure`
somewhere in its final text response, and the runner scans the output for
this marker if no `signal_complete` MCP call landed. The runner always
appends this instruction to the system prompt it sends via
`OPENCODE_CONFIG_CONTENT` (see [How It Works](#how-it-works)), regardless of
the agent config's own `system_prompt` — no manual setup needed.

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

## Effort

**Partially supported, pass-through.** The agent config's `effort` field is
mapped to `opencode run --variant <value>` — one of `low`, `medium`, `high`,
`xhigh`, `max` — passed through unchanged (no clamping, unlike `codex_cli`'s
`minimal|low|medium|high`). The flag is omitted entirely when `effort` is
unset. Unlike `claude`/`codex_cli`, there is no fixed vendor enum to
validate against: `--variant` is forwarded by opencode straight to whichever
model provider it's configured against, and the set of variants that model
actually understands is model-specific. An unsupported value may be
**silently ignored, or rejected further upstream** by the model provider —
opencode itself does not validate it. When a variant is passed, the runner
logs `requested effort=<value> (--variant)` as a `LogSystem` entry so this
can be cross-referenced against the run's actual behavior (or stderr, if
the provider rejects it). See [agents.md § Effort](../agents.md#effort).

## Image Attachments

**Supported** via `opencode run`'s `-f`/`--file` flag. Every task attachment
is passed as its own `-f <path>` argument, using the worktree copies the
dispatcher makes under `.task_attachments/` before the run starts (the
server's own upload directory is not readable by the isolated agent user
opencode runs as). Attachments are de-duplicated by that relative path, and
an attachment whose worktree copy is missing is skipped rather than failing
the whole run — the prompt still lists every attachment as a file regardless
of whether its `-f` was passed. See [How It Works](#how-it-works) for why
the `-f` arguments are always placed before the `--` separator.

opencode may reject a file type its configured model can't accept as a
message attachment; unlike `codex_cli` (`--image`), this has not been
verified live against the opencode CLI. A model-side rejection would surface
as a normal run failure in the logs, not a crash in the runner itself.

## Max Turns

**Enforced**, two ways at once, since opencode's `run` command has no CLI
turn-limit flag equivalent to `claude`'s `--max-turns`:

- **Config-side:** `max_turns` (default 50, same as the other providers when
  left at 0) is set on `agent.build.steps` in `OPENCODE_CONFIG_CONTENT` (the
  `maxSteps` key is deprecated as of opencode-ai v1.19.1; only `steps` is
  emitted). When opencode's own agent loop hits this cap it stops and prints
  `"maximum steps for this agent have been reached"`; the runner watches
  stdout/stderr for that text and treats it as a cap hit even on a clean
  exit 0.
- **Runner-side backstop:** the runner also counts `step_finish` events as
  they stream in and stops the process once the count exceeds `max_turns`,
  in case the installed opencode version ignores the `steps` config key or
  that message text changes.

Either detector returns `agent.ErrMaxTurns`, escalating the run to
`waiting_human` instead of retrying, and the step count observed is recorded
as the run's `turns_used`.

**Behavior change:** this is a new cap. `max_turns: 0` now means 50, so
existing `opencode` agent configs are newly capped at 50 steps; raise
`max_turns` explicitly if a run needs more.

## Command Allowlist / Denylist

**Enforced** via `OPENCODE_CONFIG_CONTENT`'s `permission.bash` block:

- `command_denylist` entries map to `"deny"`.
- `command_allowlist` entries (if set) map to `"allow"`, plus a catch-all
  `"*": "deny"` default — so an allowlist is a true allowlist, not merely a
  set of exceptions.
- A pattern present in both always resolves to `"deny"` — the denylist is
  the stronger signal and is never silently overridden by an allow entry for
  the same pattern.
- Empty-string patterns are skipped.

`opencode run` is never passed `--auto`, so any permission opencode would
otherwise prompt ("ask") for is auto-rejected headless — meaning a `"deny"`
entry here genuinely blocks the pattern instead of pausing for an approval
that will never come.

opencode's own pattern-matching semantics for `permission.bash` (whether
matching is most-specific-wins or first-match-wins, and whether `"*"` as a
plain map key is treated as a wildcard default rather than a literal command
named `*`) have **not been verified live** against the `opencode` binary.

## Session Resume

Every event on `opencode run --format json`'s NDJSON output carries a top-level `sessionID` field (verified against opencode-ai v1.18.6), which the runner records. When a later run on the same task hits this same agent config (and the config's `resume_sessions` flag is on — the default), the runner invokes the CLI with `--session <sessionID>`, so prior context carries forward instead of starting cold.

## Cost & Usage Reporting

**Recorded.** `input_tokens`, `output_tokens`, and `cost_usd` are read directly
from the CLI's `step_finish` event: opencode's `step-finish` part carries both
a `cost` number and a `tokens` object (`{input, output, reasoning, cache:
{read, write}}`). `classifyOpencodeJSON` reads `cost` and `tokens.input`/
`tokens.output` off that event, plus `tokens.cache.read`/`tokens.cache.write`
(folded into `input_tokens` so prompt-cache usage isn't dropped from the
total; `reasoning` is still not tracked). Cost is authoritative — reported
directly by the CLI, not estimated via a pricing table, the same as
`claude`/`qwen_code`.

`step_finish` fires once per *step*, not once per run, so a single run may
emit several of these events, and each one's `cost`/`tokens` is a **per-step
delta**, not a cumulative-to-date snapshot: opencode-ai v1.19.1's session
processor accumulates `assistantMessage.cost += step.cost` as each step
completes and emits the step-finish part with that step's own cost/tokens
only. The runner **sums `cost` and `tokens.input`/`tokens.output` (plus
`tokens.cache.read`/`tokens.cache.write` into input tokens) across every
`step_finish` in the run**, so a multi-step run's total reflects every step,
not just the last one.

Usage is persisted on every run outcome, including failed/timed-out runs —
money may have been spent on a run before it crashed.

_Verified against `opencode-ai` v1.19.1._ See [agents.md § Cost & Usage Tracking](../agents.md#cost--usage-tracking).

**Mid-run cost kill switch: not supported.** No mid-run watchdog is wired
into this provider (unlike `claude`/`qwen_code`'s incremental token-usage
watchdog) — `step_finish`'s per-step delta is only summed into a total
at end-of-run; there is no continuously-updated running total available to
a watchdog mid-run. Only the pre-dispatch `max_cost_usd` guard applies,
which is now effective since `cost_usd` is recorded. See
[agents.md § Cost Budgets](../agents.md#cost-budgets).

## Limitations

| Feature | Status |
|---|---|
| MCP tools | ✅ Available, via the inline `mcp` block in `OPENCODE_CONFIG_CONTENT` (requires `MCP_SERVER_PATH`) — see above |
| `update_task_notes` | ✅ Available |
| `store_info` | ✅ Available |
| `request_human` | ✅ Available |
| `max_turns` | ✅ Enforced via `agent.build.steps` plus a runner-side `step_finish` counter backstop — see above |
| Image attachments | ✅ Passed as one `-f <path>` per task attachment (worktree copies under `.task_attachments/`, de-duplicated, missing copies skipped) — see above |
| Outcome signalling | ✅ MCP `signal_complete`, with a text `OUTCOME: success/failure` fallback in case the tool call doesn't land (opencode's own MCP tool-naming is unverified — see [MCP Tools](#mcp-tools)) |
| Rate limit detection | ✅ Implemented — stderr, and stdout lines that aren't structured JSON events, are scanned for 429 / rate-limit signals and surfaced as `ErrRateLimit`. opencode has no typed error classification, so a rate limit reported only inside a structured event body's text isn't caught on stdout (it still surfaces via stderr or a non-zero exit) — see issue #335 |
| Cost & usage reporting | ✅ Recorded from the CLI's `step_finish` event (cost + tokens), summed across every step, authoritative — see above |
| Command allowlist/denylist | ✅ Enforced via `permission.bash` (unverified pattern-matching semantics — see above) |
| Session resume | ✅ `sessionID` + `--session` (see above) |
| Effort | ⚠️ Mapped to `--variant`, pass-through of `low/medium/high/xhigh/max`, omitted when unset; accepted values are model-specific, so an unsupported one may be silently ignored upstream — see above |

## Setup Checklist

1. Install `opencode` and add it to `PATH` (or mount it into the container)
2. Configure opencode's model and auth via its own config
3. Create a [Provider Config](../agents.md#provider-configs) with `"provider": "opencode"`, then an agent config referencing it via `provider_config_id`
4. Set `MCP_SERVER_PATH` on the backend to get the task-editor MCP tools (`get_task_transitions`, `signal_complete`, `request_human`, `update_task_notes`, `store_info`, `resolve_comment`, and `create_subtask` when enabled); without it, only the text `OUTCOME: success`/`OUTCOME: failure` marker is available for completion signalling. Either way, the runner always sends the `OUTCOME:` instruction via `OPENCODE_CONFIG_CONTENT` (see [How It Works](#how-it-works)) as a fallback; a custom `system_prompt` on the agent config is still honored (it's prepended, not replaced)

---

**A note on verification:** the `-f`/`--file` and `--variant` flag spellings
documented above (How It Works, Image Attachments, Effort) are taken from
the pinned `opencode-ai` v1.19.1 CLI's documented behavior, not confirmed
live against `opencode run --help` in this environment (the CLI is not
installed in this repo's dev container). In particular, `--variant`'s full
set of accepted values was not enumerated live — it is model/provider
dependent and opencode does not validate it CLI-side. If you have the CLI
installed, it's worth spot-checking `opencode run --help` against this doc
after an opencode CLI version bump.
