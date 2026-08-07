# Agents

## Agent Configs

An agent config connects a set of workflow labels to a specific AI provider. The dispatcher matches tasks to configs by label name.

Provider selection (which provider CLI/API, which model, and the env vars/API
keys that authenticate it) is **not** a field on the agent config itself — it
lives on a separate, reusable **Provider Config** entity that the agent config
references by id. See [Provider Configs](#provider-configs) below.

### Fields

| Field | Description |
|---|---|
| `name` | Human-readable name |
| `provider_config_id` | References a [Provider Config](#provider-configs) (provider/model/env), managed separately via `/api/v1/provider-configs`. Required. `GET`/list responses embed the resolved config as `provider_config` for convenience. |
| `labels` | JSON array of label names this agent handles (e.g. `["plan","work"]`) |
| `system_prompt` | Custom system instructions; appended with MCP tool guidance automatically |
| `max_tokens` | Maximum tokens per response (0 = provider default) |
| `timeout_secs` | Maximum run duration in seconds (0 = 600s default) |
| `max_turns` | Maximum agent turns/tool-call iterations per run (0 = 50 default). Applies to `claude`, `qwen_code`, `anthropic`, and `llm` (see the capability table below — `codex_cli`/`opencode` don't enforce it). Hitting the cap **escalates the run to `waiting_human`** with an explanatory note rather than retrying — see [Retry Policy](#retry-policy) below. |
| `max_retries` | Number of automatic consecutive retries allowed for a task after a **transient** provider error (rate limit, network blip, upstream 5xx) before it's left `failed`/escalated to `waiting_human`. `0` disables auto-retry. Default `3`. See [Retry Policy](#retry-policy) below. |
| `retry_backoff_secs` | Base backoff, in seconds, before a transient-error retry becomes eligible for re-dispatch. Exponential backoff (`base * 2^attempt`, capped at 10 minutes) is applied on top. Default `30`. |
| `enabled_plugins` | JSON array of Claude plugin IDs (`"<name>@<marketplace>"`) enabled for this config. **`claude` provider only.** Defaults to `[]` (all off). See [Claude Plugins & MCP Servers](#claude-plugins--mcp-servers) below. |
| `enabled_mcp_servers` | JSON array of Claude user-level MCP server names enabled for this config. **`claude` provider only.** Defaults to `[]` (all off). See [Claude Plugins & MCP Servers](#claude-plugins--mcp-servers) below. |
| `command_allowlist` | JSON array of shell-command glob patterns (`"*"` wildcard). If non-empty, only commands matching at least one pattern may run via `run_bash`/`Bash`. Defaults to `[]` (no restriction). **Not enforced for `opencode` or `codex_cli`.** See [Command Allowlist / Denylist](#command-allowlist--denylist) below. |
| `command_denylist` | JSON array of shell-command glob patterns (`"*"` wildcard). Commands matching any pattern here are always denied, checked before `command_allowlist`. Defaults to `[]` (no restriction). **Not enforced for `opencode` or `codex_cli`.** Enforced for `qwen_code` via `--exclude-tools` (see caveat below). See [Command Allowlist / Denylist](#command-allowlist--denylist) below. |
| `resume_sessions` | Whether new runs for a task resume the previous run's provider session instead of starting cold. **`claude`, `qwen_code`, `codex_cli`, and `opencode`** — see [Session Resume](#session-resume) below. Default on. |
| `subtasks_enabled` | Whether this config's runs may decompose their task into subtasks via the `create_subtask` MCP tool. **`claude`/`qwen_code`/`codex_cli` only.** Off by default — grant it to a specific agent (typically the planner). See [Subtasks](workflows.md#subtasks-agent-driven-decomposition). |
| `max_subtasks` | Per-parent cap on children a run may create. Default 10. |
| `max_cost_usd` | Advisory per-task cost budget cap in USD, checked by the dispatcher before each dispatch. `0` disables the cap (unlimited). Default `0`. On `claude`/`qwen_code`, also enforced mid-run as a kill switch that cancels an in-flight run over budget. See [Cost Budgets](#cost-budgets) below. |

## Provider Configs

A **Provider Config** is the provider/model/API-key (env vars) triple: which
provider CLI or API to use, which model, and the environment variables (e.g.
`ANTHROPIC_API_KEY`) that authenticate it. It's a standalone entity, managed
separately via `GET/POST /api/v1/provider-configs` and `GET/PUT/DELETE
/api/v1/provider-configs/{id}` (and the **Providers** page in the UI), so the
same provider/API-key setup can be **shared** across multiple agent configs
and chat sessions rather than each owning its own copy.

| Field | Description |
|---|---|
| `name` | Human-readable name |
| `provider` | Provider string — see [Providers](#providers) below |
| `model` | Model identifier (e.g. `claude-sonnet-4-6`, `gpt-4o`) |
| `env` | JSON object of additional environment variables merged into the provider CLI/API process's environment |

Both an agent config (`agent_configs.provider_config_id`) and a chat session
(`chat_sessions.provider_config_id`) reference a provider config by id.
Deleting a provider config is blocked with `409` while any agent config or
chat session still references it.

Existing agent configs and chat sessions created before this split are
automatically migrated to their own dedicated provider config on upgrade
(migration `039_provider_configs`) — no manual action is required.

## Providers

| Provider string | Description | MCP Tools | Details |
|---|---|---|---|
| `claude` | Claude CLI subprocess (`claude -p ...`) | ✅ All 5 (MCP sidecar) | [providers/claude.md](providers/claude.md) |
| `anthropic` **(deprecated)** | Anthropic Messages API (direct HTTP) | ⚠️ 4 of 5 native (no `resolve_comment`/`create_subtask`) | [providers/anthropic.md](providers/anthropic.md) |
| `opencode` | Opencode CLI (`opencode run --format json`) | ❌ None | [providers/opencode.md](providers/opencode.md) |
| `qwen_code` | Qwen Code CLI (`qwen -p ...`) | ✅ All 5 (MCP sidecar) | [providers/qwen_code.md](providers/qwen_code.md) |
| `codex_cli` | Codex CLI (`codex exec --json ...`) | ✅ All 5 (MCP sidecar) | [providers/codex_cli.md](providers/codex_cli.md) |
| `llm` **(deprecated)** | OpenAI-compatible API at `LLM_BASE_URL` | ⚠️ 4 of 5 native (no `resolve_comment`/`create_subtask`) | [providers/llm.md](providers/llm.md) |

> **`anthropic` and `llm` are deprecated: disabled for new/updated provider configs and may be removed in a future
> release.** They're no longer offered in the UI's provider dropdown, and `POST`/`PATCH` of a provider config using
> either is rejected. Existing provider/agent configs already using them continue to dispatch, run, and report cost
> as before. `llm` was previously also the catch-all for any unrecognized provider string (including the dead
> `openai` dropdown alias); that fallback has been removed; an unrecognized provider string now fails the run
> explicitly instead of silently being treated as an OpenAI-compatible call.

For per-provider deep-dives (credentials, tool availability, limitations, setup), see the [providers/](providers/) directory.

### Capability Matrix

A consolidated view of provider parity, replacing the scattered footnotes below. "MCP" means the tool is served over the `mcp-server` sidecar (`claude`/`qwen_code`/`codex_cli`); "native" means it's implemented directly in the Go tool-use loop (`anthropic`/`llm`, both **deprecated** — see the note above).

The table below is **generated** from [`frontend/src/lib/providerCapabilities.ts`](../frontend/src/lib/providerCapabilities.ts) — the same definition `AgentConfigForm`, `ProviderConfigForm`, and `CommandFilterEditor` read to surface these gaps inline in the UI at config time. Run `npm run gen:capability-docs` (from `frontend/`) after changing that file; do not hand-edit the table.

Two rows below aren't config-gated capabilities (no corresponding form control) and are hand-maintained: **Repo-editing tools** and **`search`/grep-style tool**.

| Capability | `claude` | `qwen_code` | `codex_cli` | `anthropic` | `llm` | `opencode` |
|---|---|---|---|---|---|---|
| Repo-editing tools | ✅ full CLI toolset | ✅ own CLI toolset | ✅ own CLI toolset | ⚠️ `read_file`/`write_file`/`str_replace`/`list_files`/`list_dir`/`search`/`run_bash` | ⚠️ same as `anthropic` | ✅ own (outside our control) |
| `search`/grep-style tool | via CLI's own tools | via CLI's own tools | via CLI's own tools | ✅ `search` (ripgrep-backed) | ✅ `search` (ripgrep-backed) | via CLI's own tools |

<!-- BEGIN capability-matrix (generated) -->

_Generated from `frontend/src/lib/providerCapabilities.ts` by `npm run gen:capability-docs` — do not hand-edit._

| Capability | `claude` | `qwen_code` | `codex_cli` | `anthropic` (deprecated) | `llm` (deprecated) | `opencode` |
|---|---|---|---|---|---|---|
| Task-editor tools (6: transitions, complete, request-human, notes, store-info, resolve-comment) | ✅ All 6 task-editor tools via the MCP sidecar (7 with create_subtask when subtasks are enabled). | ✅ All 6 task-editor tools via the MCP sidecar (7 with create_subtask when subtasks are enabled). | ✅ All 6 task-editor tools via the MCP sidecar (7 with create_subtask when subtasks are enabled). | ⚠️ 5 of 7 task-editor tools implemented natively (no resolve_comment/create_subtask). | ⚠️ 5 of 7 task-editor tools implemented natively (no resolve_comment/create_subtask). | ❌ No MCP tools — relies on a text OUTCOME: success/failure marker instead of task-editor tool calls. |
| Label / workflow transitions | ✅ | ✅ | ✅ | ✅ signal_complete implemented natively. | ✅ signal_complete implemented natively. | ❌ Cannot signal workflow transitions via MCP tools; tasks handled by this agent may not move to the next label automatically. |
| Plugins + user MCP servers | ✅ Supports Claude plugins and user-level MCP servers. | ❌ | ❌ | ❌ | ❌ | ❌ MCP servers / plugins are not supported by the opencode provider. |
| Command allowlist | ⚠️ Not an effective restriction for the claude provider: the CLI only auto-approves matches, it does not block non-matching commands. Use the denylist instead. | ❌ Not enforced for the qwen_code provider: qwen's --allowed-tools only bypasses confirmation, and the runner always passes --approval-mode yolo (auto-approve all tools), so allowlist entries have no effect. Use the denylist instead. | ❌ Not enforced for the codex_cli provider — Codex has its own native sandbox/approval-mode system instead (see docs/providers/codex_cli.md). | ✅ Enforced in Go. | ✅ Enforced in Go. | ❌ Not enforced for the opencode provider. |
| Command denylist | ✅ | ⚠️ Enforced via qwen's --exclude-tools flag (folds into its permissionsDeny policy), which is honored even under yolo mode. Per-pattern Bash(pattern) granularity mirrors --allowed-tools but has not been confirmed live for the deny path; if qwen only accepts bare tool names here, denial may degrade to blanket Bash exclusion. | ❌ Not enforced for the codex_cli provider — Codex has its own native sandbox/approval-mode system instead (see docs/providers/codex_cli.md). | ✅ Enforced in Go. | ✅ Enforced in Go. | ❌ Not enforced for the opencode provider. |
| Cost & tokens | ✅ Authoritative cost and token counts. | ⚠️ Tokens only, no cost — qwen's stream-json result carries usage but no total_cost_usd, so a cost budget cap will not reliably fire. | ⚠️ Tokens captured from turn.completed are priced via the pricing table (DB-backed, with a hardcoded fallback) — estimated, not authoritative like claude/opencode. The pre-dispatch max_cost_usd guard now works. Runs on an unpriced model are flagged "cost unknown" instead of $0. | ⚠️ Estimated from a pricing table, not authoritative. | ⚠️ Estimated from a pricing table, not authoritative. | ✅ Authoritative cost and token counts, read directly from the CLI's step_finish event (cost + tokens.input/output) — not estimated via a pricing table. |
| Mid-run cost kill switch | ✅ Mid-run kill switch: projects cost from incremental assistant-message token usage via the pricing table and cancels the run if it crosses the effective budget, escalating to waiting_human. The projection is an estimate (not the CLI's own authoritative total_cost_usd, which is only known after the run ends) — under a subscription plan with $0 real marginal cost, this estimate can still be nonzero and trigger a kill. | ⚠️ Same mid-run kill switch mechanism as claude (projects cost from incremental token usage). Only effective when the configured model is in the pricing table — otherwise the watchdog is a silent no-op and only the pre-dispatch budget guard applies. | ❌ Not implemented — usage is only known at end-of-run for this provider, so mid-run cost can't be projected. Only the pre-dispatch budget guard applies. | ❌ No mid-run kill switch implemented — only the pre-dispatch budget guard applies. | ❌ No mid-run kill switch implemented — only the pre-dispatch budget guard applies. | ❌ Usage is now recorded at end-of-run (see costTracking), but no mid-run kill switch is wired up for this provider yet — step_finish only carries a cumulative-to-date snapshot, not the per-turn incremental usage a watchdog needs to project a running total. Only the pre-dispatch budget guard applies. |
| Image attachments | ❌ The claude CLI has no --image flag (verified against v2.1.220), so this provider does not attempt to pass one. The dispatcher still copies attachments into the worktree under .task_attachments/, listed in the prompt, so agents can read them as files via the Read tool. | ❌ No image flag on the qwen CLI. | ❌ codex exec has an -i/--image flag, but attachments are not wired through to it yet. See docs/providers/codex_cli.md. | ❌ Not yet implemented. | ❌ Not yet implemented (backend-dependent). | ❌ opencode run has an -f/--file flag, but attachments are not wired through to it. |
| `max_turns` | ✅ Enforced via --max-turns. Hitting the cap escalates the run to waiting_human instead of retrying. | ✅ Enforced via --max-session-turns. Hitting the cap escalates the run to waiting_human instead of retrying. | ❌ Not enforced — codex exec has no turn-cap flag, so only the run timeout bounds a run. | ✅ Enforced via the tool-use loop. Hitting the cap escalates the run to waiting_human instead of retrying. | ✅ Enforced via the tool-use loop. Hitting the cap escalates the run to waiting_human instead of retrying. | ❌ Not enforced — the opencode CLI has no turn-cap flag. |
| Session resume | ✅ session_id + --resume. | ✅ session_id + --resume. | ✅ thread_id + codex exec resume. | ❌ Achievable (persist messages) but not yet implemented. | ❌ Achievable (persist messages) but not yet implemented. | ✅ sessionID + --session. |
| Subtasks (`create_subtask`) | ✅ create_subtask MCP tool available. | ✅ create_subtask MCP tool available. | ✅ create_subtask MCP tool available. | ❌ No create_subtask tool — not available on this provider. | ❌ No create_subtask tool — not available on this provider. | ❌ No create_subtask tool — not available on this provider. |

<!-- END capability-matrix (generated) -->

Notes:
- `anthropic`/`llm` gained `get_task_transitions`, `list_dir`, `search`, and `str_replace` as native tools; `signal_complete` now takes `outcome: "success"|"failure"` — identical to the MCP version (previously it took a raw `next_label`, which was a bug: the schema advertised `next_label` but the implementation always read `outcome`, silently dropping the model's completion signal).
- `opencode`'s MCP-via-project-config path (writing a per-run `opencode.json` pointing at the same sidecar) is unexplored; see the provider doc for current status. Until proven out, treat `opencode` as the chat-grade/experimental tier of the providers above.
- Image attachments and session-continuity-via-persisted-messages for `anthropic`/`llm` are tracked as follow-up work, not implemented here.
- **Session resume works for `claude`, `qwen_code`, `codex_cli`, and `opencode`.** `Dispatcher.resolveAgentConfig` looks up a prior session for any provider `providerSupportsResume` recognizes, and each of those four runners already records the right session/thread id and passes the CLI's resume flag correctly.
- **`max_turns` is not enforced on every provider.** `claude` (`--max-turns`), `qwen_code` (`--max-session-turns`), and `anthropic`/`llm` (the Go tool-use loop) honor it. `codex_cli` and `opencode` have no turn-cap flag, so the field is stored but has no effect — on those two, only the agent config's run timeout bounds a run.

## Dispatcher

The dispatcher runs a background goroutine that sweeps the database every 5 seconds:

1. Queries `ListAgentPickupTasks` — tasks whose label appears in any agent-triggerable transition AND whose `active_agent_run_id IS NULL`, ordered by `priority` (urgent → high → normal → low) then oldest first. See [Task Priority](#task-priority) below.
2. Computes each repo's current in-flight run count (`CountActiveRunsByRepo`) and skips any task whose repo is already at its effective concurrency limit — see [Per-Repo Concurrency Limits](#per-repo-concurrency-limits) below.
3. Loads all agent configs, matches each task to the first config whose `labels` array contains the task's label.
4. Creates an `agent_runs` record with status `pending`.
5. Sets the task's `active_agent_run_id` (and updates `current_agent_run_id`) — this prevents the next sweep from double-dispatching. The two "phantom" `waiting_human` escalation paths (cost budget exhausted/unenforceable, and provider disabled or unknown) are the exception: their synthetic run has no logs and no feedback, so they set only `active_agent_run_id` (still gating re-dispatch) and leave `current_agent_run_id` pointing at the task's last real run — the run WS replay shows and the run the next dispatch reads rework feedback from. See [Cost Budgets](#cost-budgets) below and issue #344.
6. Submits a `Job` to the worker pool. If the pool queue is full, marks the run `failed` and clears `active_agent_run_id`.

### Block Reasons

Every step above that declines to dispatch a task (paused, repo at its concurrency limit, no matching config, all matching configs rate-limited, cost budget exhausted, WIP limit full) — plus the SQL-level gates in `ListAgentPickupTasks` itself (unmet dependency, transient-retry backoff, `agent_ignore` label) — is surfaced on the task via the read-time `block_reason` field (see [api.md](api.md)'s Task fields table). `internal/agent/blockreason.go`'s `BlockReasonResolver` re-evaluates the same predicates the dispatcher uses, in the same order, so only the first reason that would actually block dispatch is reported and it can never drift from real dispatch behavior. It's computed fresh on every `GET /tasks`/`GET /tasks/{id}` response (never stored), in one batched pass per request so listing a page of tasks doesn't cost a query per task.

## Per-Repo Concurrency Limits

Each repo has an optional `max_concurrent_runs` column (nullable, editable on the Repos page). It caps how many agent runs the dispatcher will keep in flight against that repo at once, independent of how many free slots the global worker pool has:

- **Unset (`null`, the default)** — no repo-specific cap; the dispatcher falls back to the global `MAX_WORKERS` limit, preserving pre-#255 behavior exactly.
- **Set to a positive integer** — the dispatcher skips any further tasks for that repo once its in-flight run count (`active_agent_run_id IS NOT NULL`, same signal `ListAgentPickupTasks` excludes on) reaches this value, even if the pool has free workers. This prevents one repo with many eligible tasks (e.g. from a schedule or bulk GitHub Issues import) from monopolizing every worker slot and starving every other repo.
- The limit is enforced per-sweep: in-flight counts are loaded once at the start of a sweep and updated in-process as tasks are dispatched within that sweep, so a repo hitting its limit mid-sweep is skipped for the rest of that sweep too.
- The Dashboard's "Repo concurrency" section shows live in-use vs. effective-limit slots per repo (only repos with at least one in-flight run are listed).

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
| `anthropic` | Messages API `usage` field, summed across every turn of the agentic loop | `cost_usd` is *estimated* by multiplying tokens by a USD-per-1M-token price. A model's price comes from the user-editable `model_pricing` table (`GET`/`PUT /api/v1/settings/pricing`, see below) if present, otherwise from a small hardcoded fallback map (`internal/agent/providers/pricing.go`). A model in neither is left at `cost_usd = 0` and the run is flagged `cost_unknown = true` rather than silently reported as free. |
| `llm` | OpenAI-compatible `usage` field (`prompt_tokens`/`completion_tokens`), summed across every turn | Same estimation approach and pricing table as `anthropic`. |
| `opencode` | CLI's `step_finish` event (`part.cost` + `part.tokens.{input,output}`) | Authoritative — `cost` is reported directly by the CLI, not estimated. `step_finish` fires per step, not once per run; the runner takes the *last* step_finish's values (assumed cumulative-to-date, mirroring opencode's own session-level SQLite schema) rather than summing across steps. See [providers/opencode.md](providers/opencode.md#cost--usage-reporting). |
| `codex_cli` | CLI's `turn.completed` JSONL event (`usage.input_tokens`/`usage.output_tokens`) | The Codex CLI's JSON output reports no cost figure, so token counts are priced through the same estimation path (and pricing table) as `anthropic`/`llm`. A model in neither the DB-backed `model_pricing` table nor the hardcoded fallback map leaves `cost_usd = 0` and flags the run `cost_unknown = true`, same as `anthropic`/`llm`. |

### Editable pricing table

The `anthropic`/`llm` cost estimate is driven by a per-model USD-per-1M-token
price, stored in the DB-backed `model_pricing` table and editable from
**Configuration → Pricing** in the UI (or directly via `GET`/`PUT
/api/v1/settings/pricing`). The table is seeded on first migration from the
same values that used to be hardcoded, so estimates are unchanged until you
edit a row; edits take effect on the very next run completion without a
restart. A model not listed in `model_pricing` falls back to a small,
approximate hardcoded map in `internal/agent/providers/pricing.go` (exact
match, then longest model-ID-prefix match); a model matching neither has
`agent_runs.cost_unknown` set to `1` for that run (surfaced in the run
history UI as "cost unknown") instead of silently showing `$0`, so a stale
or missing price is visible rather than mistaken for a genuinely free run.
`claude`/`qwen_code`/`opencode` never set `cost_unknown` — their CLI-reported
cost (including a legitimate `$0` under a Claude Max subscription) is always
authoritative. `codex_cli` prices its captured tokens the same way as
`anthropic`/`llm` and can set `cost_unknown` for an unpriced model.

The Dashboard shows an aggregate total (tokens + cost) across all runs in a terminal state (`completed`/`failed`/`waiting_human`), plus a per-provider breakdown (via `provider_configs.provider`, joined `agent_runs.agent_config_id` → `agent_configs` → `agent_configs.provider_config_id` → `provider_configs`). The aggregate total query does not join on `agent_configs`, so it includes every terminal run regardless of its config. The per-provider breakdown *does* join on `agent_configs`/`provider_configs`, so runs whose agent config was later deleted (`agent_config_id` is set `NULL` on delete) are excluded from that breakdown, since they can no longer be attributed to a provider — a known limitation.

The Dashboard also breaks cost/usage down further into a **per-agent-config
performance table**: success rate (completed/failed/waiting_human counts),
average and p90 run duration, average "turns to done" per task, a
transient-retry snapshot, and tokens/cost — all grouped by `agent_config_id`
instead of just `provider`, so you can compare individual agent configs
against each other (e.g. "is opus-on-review worth it?") instead of only
comparing at the provider level. Same terminal-state + still-existing
`agent_config_id` filtering as the per-provider breakdown above (a run whose
config was later deleted can't be attributed to any config, per-provider or
per-config). Two known limitations apply here as well:

- **Last-run attribution.** "Turns to done" and the retry snapshot are both
  computed by attributing an entire task to the agent config of that task's
  **last** run, not by proportionally splitting the task's history across
  every config it passed through. If a task was retried under one agent
  config and then finished by a different one, all of its turns/retries are
  counted only against the config that finished it.
- **Live, resettable retry snapshot.** The retry fields read
  `tasks.transient_retry_count` as it stands right now for tasks currently
  sitting on a terminal label. That counter resets to `0` on success or
  escalation to a human, so this is a live snapshot ("how many done tasks
  currently have a nonzero retry count"), not a lifetime/historical count of
  every transient retry that config has ever triggered.

The Dashboard additionally shows a **cost-by-day table** (most recent 30
days with recorded activity, newest first) and a **"top tasks by cost"
table** (the 20 highest-cost tasks by cumulative `cost_usd` across every
run, GET `/dashboard`'s `cost_by_day`/`cost_by_task` fields). Unlike the
total/per-provider/per-agent-config breakdowns above, cost-by-day and
cost-by-task deliberately include runs in **every** status, not just
terminal ones — see [Cost Budgets](#cost-budgets) below for why. A
lightweight `GET /dashboard/cost-by-task` endpoint (no top-N cap, no task
titles) backs the Board page's "Filtered cost" badge, which sums recorded
cost across whatever tasks the current board filters leave visible. The
Task Detail page shows a task's own cumulative cost as a simple client-side
sum over its already-fetched run list (`GET /tasks/{id}/runs`), next to its
budget if one is set.

## Cost Budgets

`max_cost_usd` can be set on an agent config and/or on an individual task
to give the dispatcher an advisory spending cap. Enforcement has two layers:
a **pre-dispatch guard** (all providers) that blocks the *next* dispatch once
the budget is already exhausted, and, for providers with mid-run priced usage
(`claude`, and `qwen_code` when its configured model is priced), a **mid-run
kill switch** that cancels an *in-flight* run once its projected cost crosses
the budget — see [Mid-run kill switch](#mid-run-kill-switch) below. There's
also a configurable **early warning** event fired before either guard trips
— see [Early Warning](#early-warning) below.

- **Effective budget.** If both the task's and its matched agent config's
  `max_cost_usd` are set (nonzero), the **lower** of the two applies. If
  only one is set, that one applies. If neither is set (both `0`), there is
  no cap.
- **Cumulative cost.** The dispatcher sums `cost_usd` across **every**
  `agent_runs` row for the task, regardless of status — including failed
  and in-flight runs — not just terminal-successful ones. A task that fails
  repeatedly still accumulates real spend and shouldn't be able to dodge
  its budget by never reaching a "done" run. This is a different filter
  than the Dashboard's total/per-provider/per-agent-config aggregates,
  which only count terminal-status runs (`completed`/`failed`/`waiting_human`) —
  see [Cost & Usage Tracking](#cost--usage-tracking) above.
- **Pre-dispatch escalation.** When the budget is already exhausted at
  sweep time, the dispatcher does *not* start a provider run. Instead it
  creates a "phantom" `agent_runs` row directly in `waiting_human` status
  (no provider invocation happens) and sets it as the task's **active** run
  only (locking the task exactly like a real `waiting_human` run would), and
  publishes a `task.needs_human` WebSocket event so the Dashboard's
  intervention queue and the Task Detail page pick it up live — mirroring
  how `Pool.handleTransientFailure` escalates after a retry budget is
  exhausted. The task's label is left unchanged; `waiting_human` is a run
  status, not a workflow label. Deliberately, the task's **current** run
  pointer (`current_agent_run_id`) is left untouched, still pointing at the
  last real run: the phantom has no logs and no feedback, so it must never
  become the run WS replay shows or the run the next dispatch reads rework
  feedback from (see issue #344). The run's `notes` field (and the WS event's
  `message`) carry the exact string:

  ```
  budget exhausted: $<spent> of $<budget>
  ```

  formatted to two decimal places (e.g. `budget exhausted: $1.50 of $1.00`).

- **Recovery.** The task stays locked on the phantom (or mid-run-killed —
  see below) run until a human acts — either raising the budget (on the
  task and/or its agent config) or replying via the normal `request_human`
  reply flow, which starts a fresh run through `DispatchReply`.
  **`DispatchReply` is intentionally never budget-gated** — a human who is
  already actively intervening should never be blocked by their own budget
  check.
- **Scope.** The pre-dispatch guard only runs in the sweep dispatch path
  (`Dispatcher.dispatch`), not in `DispatchReply`.

### Mid-run kill switch

`claude` and `qwen_code` (see the capability matrix above — qwen_code only
when its configured model is in the pricing table) watch a run's token usage
as it streams and project total task cost — prior recorded spend plus this
run's incremental usage — against the effective budget while the run is
still in flight, using the same pricing table (`providers/pricing.go`) the
Dashboard's cost estimates use. When the projection crosses the budget, the
provider cancels its own subprocess immediately rather than waiting for the
run to finish, and the run is escalated straight to `waiting_human` (not
retried, not treated as a plain failure — re-dispatching would simply start
spending against the same already-exhausted budget again). The run's `notes`
field (and the WS event's `message`) carry:

```
mid-run cost budget exceeded: $<spent> of $<budget>
```

A killed run never reaches its terminal `result` stream-json event — the
only place token usage/cost is normally read from — so the provider
persists the watchdog's own cumulative-usage snapshot (input/output tokens
observed so far, and this run's own incremental cost — the projection minus
whatever prior runs had already spent) onto the run instead of leaving
`cost_usd`/tokens at `0`. This keeps `SumTaskCost` (the pre-dispatch guard
above, and the Task Detail cumulative-cost view) accurate across repeated
kill → raise-budget → resume cycles: a killed run's real spend still counts
toward the task's total, so it can't be "reset" by killing and resuming it.

**This projection is an estimate, not the provider's own authoritative
billed cost.** Claude/qwen only report a `total_cost_usd` on the terminal
`result` event of a stream-json run — by definition too late for a mid-run
kill switch to act on. The watchdog instead derives cost from incremental
`input_tokens`/`output_tokens` (folding cache tokens into the input count,
since the pricing table has no separate cache rate — see
`extractAssistantUsage` in `parse_streamjson.go`) via the same pricing table.
Two consequences:

- **Subscription plans.** Under a Claude subscription where the real
  marginal cost of a run is `$0`, the pricing-table estimate can still be
  nonzero and can still trigger a mid-run kill. If you rely on a cost budget
  under a subscription, size `max_cost_usd` generously (or leave it unset)
  rather than treating it as a precise dollar cap.
- **Unpriced models.** If the configured model isn't in the pricing table,
  the watchdog can't project a cost at all and is a silent no-op — the
  pre-dispatch guard is the only enforcement for that run (and it, too, may
  under-report if the model's cost was never priced on prior runs).

Providers without mid-run priced usage — `codex_cli`, `opencode`, and the
deprecated `anthropic`/`llm` providers — do **not** support the mid-run kill
switch (`codex_cli` now prices its captured tokens via the same pricing
table as `anthropic`/`llm` — see [Cost & Usage
Tracking](#cost--usage-tracking) above — but only once the full
`turn.completed` event arrives at the end of a run, not incrementally, so
there's nothing for a mid-run watchdog to project against;
`opencode` similarly records authoritative cost/tokens, but only at
end-of-run — its `step_finish` event carries a cumulative-to-date snapshot,
not the per-turn incremental usage a mid-run watchdog needs to project a
running total). For these, `max_cost_usd` only ever prevents the *next*
dispatch once the budget is already exhausted — a single expensive run can
still land arbitrarily far over budget before anything notices. This is
intentional and documented, not a silent gap: check the capability matrix
above (or `providerCapabilities.ts`'s `costWatchdog` entry) before relying
on `max_cost_usd` as a hard ceiling for a given provider.

### Early Warning

Independent of the hard budget guards above, a `task.cost_warning`
WebSocket event fires once a task's cumulative spend crosses a configurable
**warning threshold** — a fraction of the effective budget, default `0.8`
(80%) — giving earlier visibility than only learning about a budget at 100%.
It fires from two places:

- The **mid-run watchdog** (`claude`/`qwen_code`, as above), the first time
  its projected cost crosses the threshold during a run — even if the run
  ultimately completes under budget.
- The **dispatcher's pre-dispatch check**, for *any* provider (including
  those without watchdog support): if a task's recorded spend is already at
  or past the threshold — but not yet over budget — when a new run is about
  to be dispatched. This fires **once** per task (gated by the task's
  `cost_warned` flag) rather than on every sweep, and resets if the task's
  own `max_cost_usd` is later changed (so raising the budget lets it warn
  again as spend approaches the new ceiling).

The event payload carries `task_id`, `spent_usd`, `budget_usd`, and
(mid-run only) `run_id` — see [websocket.md](websocket.md). The board shows
a "💰 Budget warning" badge on the task card and a banner on Task Detail
while a warning is outstanding; both clear when the task's label next
changes (a new lifecycle stage).

The warning threshold itself is a single global setting (not per-task or
per-agent-config), stored in the `cost_warning_settings` table and readable/
editable via `GET`/`PUT /api/v1/settings/cost-warning` — see
[api.md](api.md). Changes take effect on the next dispatch/run check without
a restart.

## Global Cost Ceiling

Everything in [Cost Budgets](#cost-budgets) above bounds a single **task**'s
spend. Nothing bounds total spend across the whole system — three paths
create tasks automatically (the cron scheduler, GitHub/Gitea issue import,
and subtask decomposition), each arriving with a fresh, full budget, so
per-task caps do nothing about the actual risk: task *count*. Two optional
server settings close that gap:

- `MAX_DAILY_COST_USD` — a rolling cap on total recorded spend (across every
  provider/task/agent config, regardless of run status — same "every run
  counts" accounting as [Cost Budgets](#cost-budgets)) for the current
  **UTC calendar day**.
- `MAX_MONTHLY_COST_USD` — the same, for the current **UTC calendar month**.

Both default to `0` (unset/unlimited), matching how `REPO_BASE_DIR`/
`API_TOKEN` treat "not configured" — upgrading never silently halts an
existing deployment. Calendar-aligned (not a rolling window) because the
operator's mental model is a monthly bill, and it reuses the same day/month
buckets the Dashboard's cost-by-day series already uses.

**Enforcement is gate-at-dispatch, not kill-mid-run.** Once either
configured cap is reached or exceeded, the dispatcher (`Dispatcher.sweep`,
via `refreshGlobalCostStatus`) stops starting **any** new run, system-wide,
for the rest of that sweep and every sweep after until spend for the
tripped period drops back under its cap (i.e. the next UTC day/month
begins). Runs already in flight are left to finish — killing them mid-run
would waste spend already incurred on work that might be one turn from
done. If you need a harder mid-run global stop, that would be a separate,
opt-in setting; this feature intentionally doesn't do it.

**Tripping the cap does NOT escalate every task to `waiting_human`.**
Unlike the per-task guard's phantom-run escalation (see
[Cost Budgets](#cost-budgets) above), doing that at global scale would
create hundreds of phantom runs and would repeatedly clobber
`current_agent_run_id` on every affected task. Instead:

- Dispatch halts globally (no phantom runs, no per-task writes).
- A one-shot `system.cost_budget_tripped` WebSocket event fires on the
  transition into the tripped state (never re-published on every
  subsequent sweep while it stays tripped) — see
  [websocket.md](websocket.md).
- Every otherwise-eligible task's read-time `block_reason` reports
  `cost_budget_global` (see the `BlockReason` codes table in
  [api.md](api.md)) ahead of every other reason — since it applies
  identically to every task in the system at once, it's more informative
  to surface immediately than to first walk through per-task gates that
  are, at that moment, moot.
- The tripped status is surfaced loudly, not just in logs: `GET /readyz`
  (still reports `200` — a tripped cap is intentional backpressure, not a
  broken backend — but with `global_cost_tripped`/
  `global_cost_tripped_reason` fields) and `GET /api/v1/health/providers`
  (a `global_cost` object) both include it, since this is the one condition
  where the whole system has stopped doing its job while otherwise
  appearing healthy.

**Dashboard.** `GET /dashboard`'s `global_cost_budget` field (present only
when at least one cap is configured) carries the current spent/limit/
tripped snapshot plus a deliberately simple **burn-rate forecast** per
configured period — the trailing 7-day mean of recorded daily cost,
extrapolated linearly to the end of the day/month. The goal is "am I on
track to blow through this", not an accurate prediction; nothing
statistical is attempted. The Cost & Usage page renders this as a progress
bar per period (daily/monthly) plus the forecast figure, with a banner when
tripped.

**Cost-by-repo.** While wiring the plumbing above, `GET /dashboard` also
gained a `cost_by_repo` rollup (per-repo token/cost, every run status,
joined through `tasks` since `agent_runs` has no `repo_id` of its own) —
the natural companion to `cost_by_task`/`cost_by_provider` for answering
"which repo is expensive" before setting a per-repo
`repos.max_concurrent_runs` limit.

## Task Priority

Every task has a `priority` (plain `INTEGER` column, default `0`), one of
four levels:

| Value | Level |
|---|---|
| `-1` | Low |
| `0`  | Normal (default) |
| `1`  | High |
| `2`  | Urgent |

`ListAgentPickupTasks` — the query the dispatcher's sweep uses to find
eligible tasks (see [Dispatcher](#dispatcher) above) — orders its results by
`priority DESC, created_at ASC`. This only matters when there are more
eligible tasks than free `MAX_WORKERS` slots: with idle capacity, every
eligible task is dispatched anyway regardless of priority. Priority affects
**ordering only** — it does not preempt, pause, or cancel a task whose run is
already `running`, and it does not bypass any other dispatch gate (paused,
archived, blocked by an unsatisfied dependency, backed-off transient retry,
or an exhausted cost budget).

Priority can be set on task create/update via the API (`priority` field on
`POST /tasks` and `PATCH /tasks/{id}`) and edited from the board (task card
and task-detail edit forms). The board also surfaces a derived, read-time
`queue_position` on each task response — its current 0-based rank in the
priority-ordered pickup queue — shown as an "N in queue" hint on cards. It is
**only populated when the worker pool is saturated** (every `MAX_WORKERS`
slot is busy with an in-flight run); a task that is pickup-eligible but would
be dispatched immediately because a worker is free gets `null`, same as a
task that isn't pickup-eligible at all (paused, archived, blocked, etc.).
This keeps the badge meaning "actually waiting for a free worker" rather than
just "technically eligible."

## Session Resume

Each run's provider output carries a session or thread id (`session_id` on
the `claude`/`qwen_code` stream-json envelope, `sessionID` on every
`opencode` NDJSON event, `thread_id` from codex's `thread.started` event);
the pool persists it on the run row. When the dispatcher starts a new run for
a task whose **same agent config** previously recorded a session — a feedback
loop back to `work`, a re-run after a genuine failure, a reply to
`request_human` — and the config's `resume_sessions` is on, `Dispatcher.
resolveAgentConfig` looks up that prior session for any provider
`providerSupportsResume` recognizes (`claude`, `qwen_code`, `codex_cli`,
`opencode`) and passes it through so the runner resumes the same conversation
with full prior context instead of re-deriving it from the repo:
`--resume <session_id>` for `claude`/`qwen_code`, `codex exec ... resume
<thread_id> "<prompt>"` for `codex_cli`, `--session <sessionID>` for
`opencode`.

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

## MCP Tools (claude, qwen_code, and codex_cli providers)

When `MCP_SERVER_PATH` is set, the `claude`, `qwen_code`, and `codex_cli` providers launch an MCP sidecar that exposes 5 tools:

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
- **Scope:** this is currently `claude`-provider-only. Other providers (`anthropic`, `opencode`, `qwen_code`, `codex_cli`, generic `llm`) have the same DB columns available but ignore them entirely.

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
| `qwen_code` | `command_allowlist` is **not enforced** — qwen's `--allowed-tools` only bypasses its confirmation prompt (it doesn't restrict anything), and this runner always passes `--approval-mode yolo` (auto-approve all tools) on top, making an allowlist moot either way. `command_denylist` **is enforced** via `--exclude-tools Bash(pattern)` entries, which qwen folds into its `permissionsDeny` policy (honored even under `yolo`); the exact pattern-matching granularity on this flag hasn't been confirmed against a live run, so treat per-pattern denial as best-effort. See [providers/qwen_code.md](providers/qwen_code.md). |
| `opencode` | **Not enforced at all** — opencode has no Bash tool wired up server-side; it manages tool permissions via its own global config. |
| `codex_cli` | **Not enforced at all** — Codex has its own native sandbox/approval-mode system instead (`--sandbox`, `--ask-for-approval`), bypassed entirely by the `--dangerously-bypass-approvals-and-sandbox` flag this provider must pass for headless operation. See [providers/codex_cli.md](providers/codex_cli.md). |

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
- **Turn-limit exhaustion** — the agent hit its configured `max_turns` cap
  without finishing (`claude`, `qwen_code`, `anthropic`, `llm`; see the
  [`max_turns` field](#fields) above). This is neither a genuine failure nor
  a transient one: the agent didn't fail at the work, it was cut off by a
  limit the operator set on purpose. Re-dispatching would silently hand the
  next run a fresh turn budget, voiding the cap entirely. Unlike a genuine
  failure, a turn-limit run **escalates straight to `waiting_human`** with an
  explanatory note ("Agent hit its turn limit (N turns) without completing
  ...") and the task stays locked — the same as an exhausted retry/cost
  budget or an auth failure. It does **not** consume the transient-retry
  budget. To continue, raise `max_turns` on the agent config, or reply to /
  re-dispatch the run — the session id is preserved, so a resume-capable
  provider picks the conversation back up.

## Environment Variable Security

The `env` field on a [Provider Config](#provider-configs) (referenced by an agent config's `provider_config_id`, or a chat session's) passes additional env vars to the subprocess. Keys that could hijack process execution are blocked and logged as warnings:

`PATH`, `LD_PRELOAD`, `LD_LIBRARY_PATH`, `HOME`, `SHELL`, `IFS`, `DYLD_INSERT_LIBRARIES`, `DYLD_LIBRARY_PATH`

**Subprocess environment is allowlisted per provider, not inherited wholesale.**
CLI-based providers (`claude`, `qwen_code`, `codex_cli`, `opencode`) do not receive the
backend process's full environment. Each one only sees a small, hardcoded
per-provider allowlist pulled from the backend's own env (`PATH`/`HOME` for
binary resolution and credential/config lookup, a few locale/proxy/TLS-trust
vars, and that provider's specific API-key/base-URL vars — see
[docs/providers/README.md § Subprocess environment](providers/README.md#subprocess-environment)
and each provider's own doc for the exact list) plus whatever is set via the
`env` field above. Backend-only secrets like `LLM_API_KEY` (used by the
deprecated `llm`/`anthropic` providers) or the API's own `API_TOKEN` are
never visible to an agent subprocess unless explicitly re-exposed via `env`.
If an agent needs a value that isn't on its provider's allowlist, set it via
`env` rather than relying on it being present in the backend's process
environment.

This scoping also covers the **interactive chat terminal** — the
`/chat/sessions/{id}/terminal` WebSocket that runs a live `claude`/`codex`/
`qwen`/`opencode` process in a PTY per chat session (`agent.TerminalManager`
in `backend/internal/agent/terminal.go`). It has the same Bash access as a
headless run, so it is launched with the same per-provider allowlist rather
than the backend's full environment.
