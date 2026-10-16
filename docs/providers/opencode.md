# Provider: `opencode`

The `opencode` provider runs the [opencode](https://opencode.ai) CLI. **MCP tools are not supported** — opencode has no `--mcp-config` flag. Agents must signal completion by printing `OUTCOME: success` or `OUTCOME: failure` in their text output.

**Status: chat-grade / experimental** relative to `claude`/`qwen_code`/`codex_cli` — see the [capability matrix in agents.md](../agents.md#capability-matrix). Whether opencode's project-scoped config (`opencode.json`) can be used to inject the same `mcp-server` sidecar used by the other CLI providers (closing the MCP-tools and command-policy gaps below) is an open question that has not yet been investigated; until it is, treat this provider as second-class for task-editor integration purposes even though its own repo-editing toolset is otherwise capable.

## Provider String

```
"provider": "opencode"
```

## How It Works

Runs: `opencode run --format json [-m <model>] [--session <id>] [--variant <effort>] [-f <file> ...] -- <prompt>`

Output is parsed as NDJSON. The `--` separator prevents the prompt content from being interpreted as CLI flags. The `-f <file>` arguments (one per task attachment, see [Image Attachments](#image-attachments)) are always placed *before* the `--` separator — `-f`/`--file` is a variadic (array-type) option in opencode's yargs-based CLI, so a bare `-f a.png "<prompt>"` would swallow the prompt as a second file; repeating `-f` per path and keeping `--` immediately before the prompt guards against that.

`opencode run` has no `--system-prompt`/`-p` flag, so the system prompt
(the agent config's own `system_prompt`, plus the task-editor boilerplate —
critically, the instruction to end with `OUTCOME: success`/`OUTCOME:
failure`) is delivered via the `OPENCODE_CONFIG_CONTENT` environment
variable (supported in opencode-ai v1.19.1), set on the `build` agent's
`prompt` field — the agent `opencode run` uses when none is otherwise
specified. This env var is generated per run by the runner itself; it is
not part of the subprocess environment allowlist described below, and it
always overrides any `OPENCODE_CONFIG_CONTENT` set in the agent config's own
`env` field.

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

**Not supported.** Opencode has no `--mcp-config` flag. The MCP sidecar is not launched for this provider.

This means:
- `update_task_notes` — **unavailable**
- `store_info` — **unavailable**
- `signal_complete` (MCP version) — **unavailable**
- `request_human` (MCP version) — **unavailable**

**Workaround for completion signalling:** The agent must output `OUTCOME: success` or `OUTCOME: failure` somewhere in its final text response. The runner scans the output for this marker. The runner always appends this instruction to the system prompt it sends via `OPENCODE_CONFIG_CONTENT` (see [How It Works](#how-it-works)), regardless of the agent config's own `system_prompt` — no manual setup needed.

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

The `max_turns` agent config field is accepted but **not currently enforced** by this provider — the opencode CLI's `run` command has no documented turn-limit flag equivalent to `claude`'s `--max-turns`. The field is stored and available for a future opencode CLI feature, but has no effect today.

## Command Allowlist / Denylist

**Not enforced.** The `command_allowlist`/`command_denylist` agent config fields have
no effect for this provider — opencode has no Bash tool wired up server-side (it
manages its own tool permissions via its own global config, outside task-editor's
control). If you need to restrict shell command execution for an agent config, use
the `claude`, `qwen_code` or `codex_cli` providers instead.

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
| MCP tools | ❌ Not available |
| `update_task_notes` | ❌ Not available |
| `store_info` | ❌ Not available |
| `request_human` | ❌ Not available |
| Image attachments | ✅ Passed as one `-f <path>` per task attachment (worktree copies under `.task_attachments/`, de-duplicated, missing copies skipped) — see above |
| Outcome signalling | ⚠️ Text-based only (`OUTCOME: success/failure`) |
| Rate limit detection | ✅ Implemented — stderr, and stdout lines that aren't structured JSON events, are scanned for 429 / rate-limit signals and surfaced as `ErrRateLimit`. opencode has no typed error classification, so a rate limit reported only inside a structured event body's text isn't caught on stdout (it still surfaces via stderr or a non-zero exit) — see issue #335 |
| Cost & usage reporting | ✅ Recorded from the CLI's `step_finish` event (cost + tokens), summed across every step, authoritative — see above |
| Command allowlist/denylist | ❌ Not enforced |
| Session resume | ✅ `sessionID` + `--session` (see above) |
| Effort | ⚠️ Mapped to `--variant`, pass-through of `low/medium/high/xhigh/max`, omitted when unset; accepted values are model-specific, so an unsupported one may be silently ignored upstream — see above |

## Setup Checklist

1. Install `opencode` and add it to `PATH` (or mount it into the container)
2. Configure opencode's model and auth via its own config
3. Create a [Provider Config](../agents.md#provider-configs) with `"provider": "opencode"`, then an agent config referencing it via `provider_config_id`
4. Nothing else needed for outcome signalling — the runner always sends the `OUTCOME: success`/`OUTCOME: failure` instruction via `OPENCODE_CONFIG_CONTENT` (see [How It Works](#how-it-works)); a custom `system_prompt` on the agent config is still honored (it's prepended, not replaced)

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
