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
| `fallback_provider_config_ids` | Ordered list of further provider configs to try, after `provider_config_id`, when the ones before them are at their usage limit or paused. Default `[]` (primary only). Each must exist, be distinct and differ from the primary; changing the primary to one in the list drops it from the list. See [Provider list](#provider-list). |
| `escalation_ladder` | Ordered rungs used when a task keeps failing on this agent's stage. Default `[]` (never escalate). See [Escalation ladder](#escalation-ladder). |
| `labels` | JSON array of label names this agent handles (e.g. `["plan","work"]`) |
| `system_prompt` | Custom system instructions; appended with MCP tool guidance automatically |
| `max_tokens` | Maximum tokens per response (0 = provider default) |
| `timeout_secs` | Maximum run duration in seconds (0 = 600s default) |
| `max_turns` | Maximum agent turns/tool-call iterations per run (0 = 50 default). Enforced on every provider (see the capability table below): `claude`/`qwen_code` via a CLI flag, `codex_cli`/`opencode` by the runner counting completed tool steps / `step_finish` events (`opencode` also sets it config-side via `agent.<name>.steps`). Hitting the cap **escalates the run to `waiting_human`** with an explanatory note rather than retrying — see [Retry Policy](#retry-policy) below. To tune this against reality rather than guesswork, each run records the turns it actually used (`turns_used` on the run object, surfaced in the task's run history and aggregated per agent config on the Dashboard). |
| `max_retries` | Number of automatic consecutive retries allowed for a task after a **transient** provider error (rate limit, network blip, upstream 5xx) before it's left `failed`/escalated to `waiting_human`. `0` disables auto-retry. Default `3`. See [Retry Policy](#retry-policy) below. |
| `retry_backoff_secs` | Base backoff, in seconds, before a transient-error retry becomes eligible for re-dispatch. Exponential backoff (`base * 2^attempt`, capped at 10 minutes) is applied on top. Default `30`. |
| `enabled_plugins` | JSON array of Claude plugin IDs (`"<name>@<marketplace>"`) enabled for this config. **`claude` provider only.** Defaults to `[]` (all off). See [Claude Plugins & MCP Servers](#claude-plugins--mcp-servers) below. |
| `enabled_mcp_servers` | JSON array of Claude user-level MCP server names enabled for this config. **`claude` provider only.** Defaults to `[]` (all off). See [Claude Plugins & MCP Servers](#claude-plugins--mcp-servers) below. |
| `command_allowlist` | JSON array of shell-command glob patterns (`"*"` wildcard). If non-empty, only commands matching at least one pattern may run via `run_bash`/`Bash`. Defaults to `[]` (no restriction). **Not enforced for `codex_cli`.** Enforced for `opencode` via a `permission.bash` block (emitted in last-match-wins order). See [Command Allowlist / Denylist](#command-allowlist--denylist) below. |
| `command_denylist` | JSON array of shell-command glob patterns (`"*"` wildcard). Commands matching any pattern here are always denied, checked before `command_allowlist`. Defaults to `[]` (no restriction). **Not enforced for `codex_cli`.** Enforced for `qwen_code` via `--exclude-tools` and for `opencode` via a `permission.bash` map (see caveats below). See [Command Allowlist / Denylist](#command-allowlist--denylist) below. |
| `resume_sessions` | Whether new runs for a task resume the previous run's provider session instead of starting cold. **`claude`, `qwen_code`, `codex_cli`, and `opencode`** — see [Session Resume](#session-resume) below. Default on. |
| `subtasks_enabled` | Whether this config's runs may decompose their task into subtasks via the `create_subtask` MCP tool. **`claude`/`qwen_code`/`codex_cli` only.** Off by default — grant it to a specific agent (typically the planner). See [Subtasks](workflows.md#subtasks-agent-driven-decomposition); when to fan out: [Planner prompt guidance](workflows.md#planner-prompt-guidance). |
| `max_subtasks` | Per-parent cap on children a run may create. Default 10. |
| `max_cost_usd` | Advisory per-task cost budget cap in USD (cumulative across all runs of a task, not per run), checked by the dispatcher before each dispatch. `0` disables the cap (unlimited). Default `0`. On `claude`/`qwen_code`, also enforced mid-run as a kill switch that cancels an in-flight run over budget. See [Cost Budgets](#cost-budgets) below. |
| `effort` | Optional reasoning effort level: `""` (default/unset), `low`, `medium`, `high`, `xhigh`, or `max`. **`claude` (full), `codex_cli` (partial, clamped), and `opencode` (partial, pass-through via `--variant`)** — see [Effort](#effort) below. Ignored by `qwen_code`. |

## Escalation ladder

Start cheap, escalate on failure. `escalation_ladder` is a JSON array of rungs `{provider_config_id, effort?, fallback_provider_config_ids?}`. **Rung 0 is implicit** — the agent's own `provider_config_id`, `effort` and `fallback_provider_config_ids`; `escalation_ladder[i]` is rung `i+1`. A rung's empty `effort` inherits the agent's. An empty ladder (the default) behaves exactly as before. Max 10 rungs; repeating a provider across rungs is fine (same model, higher effort).

**Rung selection** (at dispatch): `failed attempts = consecutive_failure_count + rework bounces`, rung = `min(failed attempts, len(ladder))`.

- `consecutive_failure_count` is the existing circuit-breaker counter: failed runs, failed verify commands and rejected transitions in the current label. It resets on any label transition, human reply or human rerun.
- Rework bounces are failure-path transitions back into this agent's label (e.g. review → work), counted newest to oldest. The count resets at a human-triggered transition or when the stage was freshly entered by a non-failure transition.
- Because the breaker stops at 3 in-label failures and the rework-loop guard at 3 bounces, rungs beyond about 3 are rarely reachable.

**Rate limits** still apply on top: for rung r ≥ 1 the providers tried are the rung's provider, its `fallback_provider_config_ids`, then the agent's own provider list. The rung stays r; its effort applies to whichever provider is used.

**Sessions**: resume is keyed on the provider config, so moving to a different provider config starts a fresh session, while a rung that only changes effort on the same provider config may resume. A human reply keeps the asking run's rung and provider.

**Visibility**: each run records `ladder_rung` (NULL for agents without a ladder), shown in Task Detail's run list (e.g. `rung 2: opus / max`) and summarised on Agent Performance as "Finished by rung". The UI form (Advanced → Escalate on failure) edits each rung's provider and effort; per-rung fallbacks are API/MCP only.


## Version history

Every behavioural change to an agent config is saved as a numbered snapshot in `agent_config_versions` (v1, v2, ...), so a prompt or model edit can be measured before/after.

- **Snapshotted:** `system_prompt`, `provider_config_id`, `fallback_provider_config_ids`, `escalation_ladder`, `effort`, `permission_mode`, `max_turns`, `max_tokens`, `timeout_secs`, `max_cost_usd`, `max_retries`, `retry_backoff_secs`, `resume_sessions`, `subtasks_enabled`, `max_subtasks`, plugins / MCP servers and the command allow/deny lists.
- **Not versioned:** `name`, `labels`, `enabled` and `priority` (identity / routing). Changing only those, or re-saving identical values, never creates a version.
- Created through the one update path, so the API, the UI and the board MCP `update_agent` all behave the same. Each version records the actor (when authenticated) and an optional `note` (the "Change note" field on the agent page, or `note` in the request body).
- **Backfill:** the migration writes v1 for every existing config; a config that still has no history (e.g. inserted by other means) gets a baseline v1 lazily on its first update or history read.
- **Run tagging:** each run records `agent_config_version` (the latest version when it was dispatched). `NULL` means the run predates version history.
- **Restore:** `POST /agents/{id}/versions/{version}/restore` applies a snapshot onto the current config (name, labels, enabled, priority are kept) and records it as a **new** version ("Restored from vN"). Provider configs referenced by the snapshot are re-validated (`400` if one was deleted). Restoring something identical to the current config writes nothing.
- **UI:** the History panel on the agent page lists versions with date, actor, note, a prompt diff against the previous version, and Restore (in-page confirm).
- **Agent Performance** can break metrics down by version, provider config, repo or task type (`group_by`); rows with fewer than 5 tasks done are greyed out as low-sample.

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
| `env` | JSON object of additional environment variables merged into the provider CLI/API process's environment. **Write-only over the API** — see [Environment Variable Security](#environment-variable-security) below: reads always mask every value as `"***"` (key names stay visible), and writes treat `"***"` as "keep the existing value", any other value as an overwrite, and an omitted key as a delete. |

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
| `opencode` | Opencode CLI (`opencode run --format json`) | ✅ All 5 (MCP sidecar, via inline `OPENCODE_CONFIG_CONTENT`) | [providers/opencode.md](providers/opencode.md) |
| `qwen_code` | Qwen Code CLI (`qwen -p ...`) | ✅ All 5 (MCP sidecar) | [providers/qwen_code.md](providers/qwen_code.md) |
| `codex_cli` | Codex CLI (`codex exec --json ...`) | ✅ All 5 (MCP sidecar) | [providers/codex_cli.md](providers/codex_cli.md) |

> **`anthropic` and `llm` were removed in v0.17.0.** `POST`/`PATCH` of a provider config using either (or the old
> `openai` alias) is rejected. Legacy configs that still reference them are kept, but their runs fail at dispatch:
> the task escalates to `waiting_human` with "provider X was removed in v0.17.0; switch this agent config to
> claude/codex_cli/opencode/qwen_code", and `GET /health/providers` reports a `removed_provider_X` error. An
> unrecognized provider string is handled the same way.

For per-provider deep-dives (credentials, tool availability, limitations, setup), see the [providers/](providers/) directory.

### Capability Matrix

A consolidated view of provider parity, replacing the scattered footnotes below. "MCP" means the tool is served over the `mcp-server` sidecar (`claude`/`qwen_code`/`codex_cli`).

The table below is **generated** from [`frontend/src/lib/providerCapabilities.ts`](../frontend/src/lib/providerCapabilities.ts) — the same definition `AgentConfigForm`, `ProviderConfigForm`, and `CommandFilterEditor` read to surface these gaps inline in the UI at config time. Run `npm run gen:capability-docs` (from `frontend/`) after changing that file; do not hand-edit the table.

<!-- BEGIN capability-matrix (generated) -->

_Generated from `frontend/src/lib/providerCapabilities.ts` by `npm run gen:capability-docs` — do not hand-edit._

| Capability | `claude` | `qwen_code` | `codex_cli` | `opencode` |
|---|---|---|---|---|
| Task-editor tools (6: transitions, complete, request-human, notes, store-info, resolve-comment) | ✅ All 6 task-editor tools via the MCP sidecar (7 with create_subtask when subtasks are enabled). | ✅ All 6 task-editor tools via the MCP sidecar (7 with create_subtask when subtasks are enabled). | ✅ All 6 task-editor tools via the MCP sidecar (7 with create_subtask when subtasks are enabled). | ✅ All 6 task-editor tools via the MCP sidecar (7 with create_subtask when subtasks are enabled), delivered through OPENCODE_CONFIG_CONTENT's inline "mcp" config since opencode has no --mcp-config flag. The text OUTCOME: success/failure marker is kept as a fallback in case the MCP tool call does not land; opencode's own tool-naming has not been verified live, which is exactly why that fallback still matters. |
| Label / workflow transitions | ✅ | ✅ | ✅ | ✅ |
| Plugins + user MCP servers | ✅ Supports Claude plugins and user-level MCP servers. | ❌ | ❌ | ❌ The task-editor sidecar is now injected (see taskEditorTools), but user-level MCP servers/plugins are still not wired through for this provider. |
| Command allowlist | ⚠️ Not an effective restriction for the claude provider: the CLI only auto-approves matches, it does not block non-matching commands. Use the denylist instead. | ❌ Not enforced for the qwen_code provider: qwen's --allowed-tools only bypasses confirmation, and the runner always passes --approval-mode yolo (auto-approve all tools), so allowlist entries have no effect. Use the denylist instead. | ❌ Not enforced for the codex_cli provider — Codex has its own native sandbox/approval-mode system instead (see docs/providers/codex_cli.md). | ⚠️ Mapped to opencode's "permission.bash" config: listed patterns get "allow" after a catch-all "*": "deny" default. Rules are emitted in opencode's last-match-wins order (https://opencode.ai/docs/permissions/): "*" first, allow entries next, deny entries last. |
| Command denylist | ✅ | ⚠️ Enforced via qwen's --exclude-tools flag (folds into its permissionsDeny policy), which is honored even under yolo mode. Verified against the 0.25.2 bundle: qwen's shell tool is named run_shell_command (not Bash), so each pattern is sent as run_shell_command(pattern) — a Bash(pattern) entry, as used for --allowed-tools above, would be a silent no-op here. qwen's own matching is exact-or-prefix, not a true glob: a trailing "*" wildcard (this app's shared glob convention) is normalized to the literal prefix qwen compares against, but a "*" anywhere else in the pattern (e.g. suffix-matching "* --force") has no qwen-side equivalent and won't match. | ❌ Not enforced for the codex_cli provider — Codex has its own native sandbox/approval-mode system instead (see docs/providers/codex_cli.md). | ⚠️ Mapped to opencode's "permission.bash" config as "deny" entries emitted last, so under opencode's last-match-wins evaluation they always win over an overlapping allowlist entry. opencode run auto-rejects "ask" permissions headless since --auto is deliberately never passed, so a deny entry genuinely blocks the pattern. |
| Cost & tokens | ✅ Authoritative cost and token counts. | ⚠️ Estimated, not authoritative: qwen's stream-json result carries usage but no total_cost_usd, so tokens are priced via the same DB-backed pricing table codex_cli uses (cached tokens, when reported, at the cached rate). If the model isn't set, the model qwen actually used (read from the assistant message) is priced instead. Runs on an unpriced model are flagged "cost unknown" instead of $0 — add a row for your Qwen model under Settings → Pricing. | ⚠️ Estimated, not authoritative like claude/opencode: tokens from turn.completed are priced via the pricing table (DB-backed, with a hardcoded fallback), with cached input tokens at the cached rate. With no model set, the model codex actually used is read from its session file. The pre-dispatch max_cost_usd guard works. Runs on an unpriced model are flagged "cost unknown" instead of $0. | ✅ Authoritative cost and token counts, summed across every step_finish event in the run (cost + tokens.input/output, plus cache read/write into input tokens) — not estimated via a pricing table. |
| Mid-run cost kill switch | ✅ Mid-run kill switch: projects cost from incremental assistant-message token usage via the pricing table and cancels the run if it crosses the effective budget, escalating to waiting_human. The projection is an estimate (not the CLI's own authoritative total_cost_usd, which is only known after the run ends) — under a subscription plan with $0 real marginal cost, this estimate can still be nonzero and trigger a kill. | ⚠️ Same mid-run kill switch mechanism as claude (projects cost from incremental token usage). Only effective when the configured model is in the pricing table — otherwise the watchdog is a silent no-op and only the pre-dispatch budget guard applies. | ❌ Not implemented — usage is only known at end-of-run for this provider, so mid-run cost can't be projected. Only the pre-dispatch budget guard applies. | ❌ Usage is now recorded at end-of-run (see costTracking), but no mid-run kill switch is wired up for this provider yet — each step_finish only carries that step's own cost/token delta, summed into a total only once the run ends, not a continuously-updated running total a watchdog could project from mid-run. Only the pre-dispatch budget guard applies. |
| Image attachments | ❌ The claude CLI has no --image flag (verified against v2.1.220), so this provider does not attempt to pass one. The dispatcher still copies attachments into the worktree under .task_attachments/, listed in the prompt, so agents can read them as files via the Read tool. | ⚠️ The qwen CLI has no --image flag, but its headless -p prompt supports @path file references the same way the interactive CLI does (verified against the 0.25.2 bundle: handleAtCommand runs unconditionally on the initial prompt in headless mode). Each attachment's worktree copy under .task_attachments/ is referenced with an @ token, and qwen embeds it as real image content — but only if the configured model's modalities report image support; otherwise qwen itself skips the file with a warning and the run continues without it. | ✅ Passed to codex exec as --image (from the worktree copies under .task_attachments/), so the model sees them visually. Also listed in the prompt as files. | ✅ Passed to opencode run as -f/--file (from the worktree copies under .task_attachments/), so the model receives them as message attachments. Also listed in the prompt as files. |
| `max_turns` | ✅ Enforced via --max-turns. Hitting the cap escalates the run to waiting_human instead of retrying. | ✅ Enforced via --max-session-turns. Hitting the cap escalates the run to waiting_human instead of retrying (detected from the CLI's stderr message, since qwen exits non-zero rather than emitting a structured result). | ✅ Enforced by the runner (codex exec has no turn-cap flag): each completed tool step (command, file edit, MCP call, web search) counts as a turn, and passing the cap stops the run and escalates it to waiting_human instead of retrying. | ✅ Enforced via agent.<name>.steps in OPENCODE_CONFIG_CONTENT, with the runner also counting step_finish events as a backstop in case the installed opencode version ignores the config key. Hitting the cap escalates the run to waiting_human instead of retrying. |
| Session resume | ✅ session_id + --resume. | ✅ session_id + --resume. | ✅ thread_id + codex exec resume. | ✅ sessionID + --session. |
| Subtasks (`create_subtask`) | ✅ create_subtask MCP tool available. | ✅ create_subtask MCP tool available. | ✅ create_subtask MCP tool available. | ✅ create_subtask MCP tool available. |
| Effort (reasoning level) | ✅ Passed as --effort. Supports low/medium/high/xhigh/max. Not all models support effort levels, and higher levels may be restricted by your Anthropic organization — either case degrades silently (the CLI only warns), so verify against the agent run logs. | ❌ No reasoning-effort flag on the qwen CLI — the field is ignored for this provider. | ⚠️ Mapped to the model_reasoning_effort config override (minimal/low/medium/high). Codex has no xhigh/max tier, so those clamp down to high. | ⚠️ Passed as opencode run --variant. The value is forwarded to the model provider as-is (low/medium/high/xhigh/max); a model that doesn't define that variant may ignore or reject it. |
| Permission mode | ✅ Passed as --permission-mode. Leave as Default (unset) to keep today's behavior (no flag — the CLI's own "auto" classifier). command_denylist is still enforced even under bypassPermissions (verified empirically). | ❌ qwen_code always runs in --approval-mode yolo (full auto-approve) — always full bypass, not configurable per agent config. | ❌ codex_cli always runs with --dangerously-bypass-approvals-and-sandbox — always full bypass, not configurable per agent config. | ❌ No permission-mode flag on the opencode CLI — the field is ignored for this provider. |
| Skills library | ✅ Skills library entries are copied into .claude/skills/<name>/ in the worktree (git-excluded). | ✅ Skills library entries are copied into .qwen/skills/<name>/ in the worktree (git-excluded). | ✅ Skills library entries are copied into .agents/skills/<name>/ in the worktree (git-excluded). | ✅ Skills library entries are copied into .opencode/skills/<name>/ in the worktree (git-excluded). |

<!-- END capability-matrix (generated) -->

Notes:
- `opencode`'s MCP tools are delivered via its inline `OPENCODE_CONFIG_CONTENT` config (no `--mcp-config` flag exists), reusing the same sidecar as the other providers; see the provider doc for the current status, including the unverified MCP tool-naming caveat.
- **Session resume works for `claude`, `qwen_code`, `codex_cli`, and `opencode`.** `Dispatcher.resolveAgentConfig` looks up a prior session for any provider `providerSupportsResume` recognizes, and each of those four runners already records the right session/thread id and passes the CLI's resume flag correctly.
- **`max_turns` is enforced on every provider.** `claude` (`--max-turns`) and `qwen_code` (`--max-session-turns`) honor it through a CLI flag. `codex_cli` and `opencode` have no such flag, so their runners count completed tool steps / `step_finish` events (commands, file edits, MCP calls, web searches for `codex_cli`) and stop the run once the count passes the cap; `opencode` also sets the cap config-side via `agent.<name>.steps` in `OPENCODE_CONFIG_CONTENT` as a second line of defense.

## Dispatcher

The dispatcher runs a background goroutine that sweeps the database every 5 seconds:

1. Queries `ListAgentPickupTasks` — tasks whose label appears in any agent-triggerable transition AND whose `active_agent_run_id IS NULL`, ordered by `priority` (urgent → high → normal → low) then oldest first. See [Task Priority](#task-priority) below.
2. Computes each repo's current worker-slot usage (`CountActiveRunsByRepo`: tasks whose active run is `pending` or `running`) and skips any task whose repo is already at its effective concurrency limit — see [Per-Repo Concurrency Limits](#per-repo-concurrency-limits) below.
3. Loads all agent configs and matches each task to the first config whose `labels` array contains the task's label, then picks the first provider in that config's [provider list](#provider-list) that is not at its limit or paused. When none is, it tries the next matching config by `priority`.
4. Creates an `agent_runs` record with status `pending`.
5. Sets the task's `active_agent_run_id` (and updates `current_agent_run_id`) — this prevents the next sweep from double-dispatching. The two "phantom" `waiting_human` escalation paths (cost budget exhausted/unenforceable, and provider disabled or unknown) are the exception: their synthetic run has no logs and no feedback, so they set only `active_agent_run_id` (still gating re-dispatch) and leave `current_agent_run_id` pointing at the task's last real run — the run WS replay shows and the run the next dispatch reads rework feedback from. See [Cost Budgets](#cost-budgets) below and issue #344.
6. Submits a `Job` to the worker pool. If the pool queue is full, marks the run `failed` and clears `active_agent_run_id`.

### Block Reasons

Every step above that declines to dispatch a task (paused, repo at its concurrency limit, no matching config, all matching configs rate-limited, cost budget exhausted, WIP limit full) — plus the SQL-level gates in `ListAgentPickupTasks` itself (unmet dependency, transient-retry backoff, `agent_ignore` label) — is surfaced on the task via the read-time `block_reason` field (see [api.md](api.md)'s Task fields table). `internal/agent/blockreason.go`'s `BlockReasonResolver` re-evaluates the same predicates the dispatcher uses, in the same order, so only the first reason that would actually block dispatch is reported and it can never drift from real dispatch behavior. It's computed fresh on every `GET /tasks`/`GET /tasks/{id}` response (never stored), in one batched pass per request so listing a page of tasks doesn't cost a query per task.

### Provider list

An agent config runs on `provider_config_id` first and then, in order, on each
of `fallback_provider_config_ids`. The prompt, labels, budgets and tools stay
in one agent config, so moving between providers doesn't need a copy of the
config per provider. At dispatch the first provider whose account (usage pool)
is not rate-limited and whose kind is not paused by the
[provider circuit breaker](#provider-circuit-breaker) is used, and the run
records it in `agent_runs.provider_config_id`. Only when every provider in the
list is unavailable does the dispatcher move on to the next matching agent
config, and with none left the task waits with the "all matching configs
rate-limited" block reason. A provider that comes back is preferred again on
the next dispatch, because it is earlier in the list; runs already going on a
later provider finish there.

Session resume (`resume_sessions`) is keyed by provider as well as agent
config, so a task that moved to another provider starts cold there instead of
handing that CLI a session id it doesn't know. The worktree and branch carry
the work over.

Settings that only some providers support (plugins, `effort`,
`permission_mode`, MCP servers, command filters, ...) apply where the provider
supports them and are ignored elsewhere. The agent config editor warns, per
provider in the list, about the settings that provider will ignore.

## Per-Repo Concurrency Limits

Each repo has an optional `max_concurrent_runs` column (nullable, editable on the Repos page). It caps how many agent runs the dispatcher will keep pending or running against that repo at once, independent of how many free slots the global worker pool has:

- **Unset (`null`, the default)** — no repo-specific cap; the dispatcher falls back to the global `MAX_WORKERS` limit, preserving pre-#255 behavior exactly.
- **Set to a positive integer** — the dispatcher skips any further tasks for that repo once its count of pending/running runs reaches this value (tasks parked on `waiting_human`, including cost-budget and other escalations, keep their re-dispatch lock but occupy no worker, so they do not count), even if the pool has free workers. This prevents one repo with many eligible tasks (e.g. from a schedule or bulk GitHub Issues import) from monopolizing every worker slot and starving every other repo.
- The limit is enforced per-sweep: usage counts are loaded once at the start of a sweep and updated in-process as tasks are dispatched within that sweep, so a repo hitting its limit mid-sweep is skipped for the rest of that sweep too.
- The Dashboard's "Repo concurrency" section shows live in-use vs. effective-limit slots per repo (only repos with at least one pending/running run are listed).

## Worker Pool

The pool manages `MAX_WORKERS` concurrent goroutines (default 5). Each worker:

1. Sets the run status to `running`.
2. Calls `Provider.Run()` which streams `LogEntry` values on a channel.
3. Persists log entries to `agent_logs` in batched transactions (flush every 500ms or every 50 entries).
4. Simultaneously publishes each entry to the WebSocket hub for live streaming; the published entry carries the same id as the persisted `agent_logs` row, so clients can dedupe live entries against replay/pagination.
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

### Escalation reasons

Every run that ends in `waiting_human` records a closed-set `escalation_reason`
(returned on the run as `escalation_reason`, on `GET /needs-me` items, and as
`reason` in the `task.needs_human` event). The human-readable explanation is in
the run's `notes` (the agent's own question for `agent_question` is in
`feedback`). All paths share one helper (`internal/agent/escalate.go`), which
writes the status, reason and message, applies the lock handling each path
already had, and publishes the event; the lock itself is never cleared by an
escalation.

| Code | Cause | UI primary action |
|---|---|---|
| `agent_question` | Agent called `request_human` | Reply |
| `rework_loop` | The same failure/rework transition fired repeatedly | Reply with guidance |
| `auth_required` | Provider CLI is not logged in | Open Health, then Retry |
| `cost_budget` | Task cost budget exhausted (pre- or mid-run) | Raise budget, then Retry |
| `cost_unknown` | Budget set but a run's cost is unpriced | Add pricing, then Retry |
| `provider_missing` | Agent config's provider is disabled or unknown | Open providers, then Retry |
| `runtime_prep` | Pinned toolchain install failed | Open repos, then Retry |
| `max_turns` | Agent hit `max_turns` | Raise max turns, then Retry |
| `repeated_failure` | Consecutive failures hit the cap | Retry |
| `verify_error` | Verify command could not be run | Check the repo's verify command, then Retry |
| `transient_exhausted` | Transient-retry budget exhausted | Retry |
| `no_outcome` | Run finished without a usable outcome | Retry |
| `rejected_transition` | Agent's requested move was refused repeatedly | Retry |

Runs escalated before this field existed have a null reason and are treated as
a question (Reply).

### Provider circuit breaker

An `auth_required` escalation also trips a per-provider circuit breaker so one
broken login doesn't escalate every queued task in turn. The breaker is keyed
by provider **kind** (`claude`, `codex_cli`, ...), not by agent config, because
the login is shared by every config of that kind and the Health checks are per
kind too. While a kind is paused the dispatcher skips agent configs of that kind
(falling over to a lower-priority config of another kind if one matches) and
leaves the tasks queued, not escalated. Human replies are never gated.

The pause lifts when that provider's existing Health check (`internal/health`)
passes, re-checked every 30s once the pause is at least 30s old, or when someone
clicks **Resume** in the app-wide banner (`POST /health/providers/{provider}/resume`).
The state is in memory only: a restart clears it, and the next auth failure
re-trips it. Because Health checks are heuristic (credentials present, not a live
token validation), an expired-but-present token can resume and re-trip; each
cycle costs at most one escalated task. See `GET /health/providers`
(`paused_providers`) and the `provider.pause_changed` WebSocket event.

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
| `qwen_code` | Same `result` envelope parsing as `claude` (`classifyStreamJSON`) | Token counts are authoritative, but the Qwen Code CLI reports **no cost figure at all** — `cost_usd` is estimated from tokens via the same DB-backed `model_pricing` table as `codex_cli` (cached tokens, when reported, at the cached rate). A model in neither table leaves `cost_usd = 0` and flags the run `cost_unknown = true`. See [providers/qwen_code.md](providers/qwen_code.md#cost--usage-reporting). |
| `opencode` | CLI's `step_finish` event (`part.cost` + `part.tokens.{input,output,cache.read,cache.write}`) | Authoritative — `cost` is reported directly by the CLI, not estimated. `step_finish` fires per step, not once per run, and each one's `cost`/`tokens` is a per-step delta (verified against opencode-ai v1.19.1); the runner sums `cost` and `tokens.input`/`tokens.output` (plus cache read/write into input tokens) across every step_finish in the run. See [providers/opencode.md](providers/opencode.md#cost--usage-reporting). |
| `codex_cli` | CLI's `turn.completed` JSONL event (`usage.input_tokens`/`cached_input_tokens`/`output_tokens`), or Codex's session file when a run stops mid-turn | The Codex CLI's JSON output reports no cost figure, so token counts are priced through an estimation path using the DB-backed `model_pricing` table (falling back to a hardcoded map), with cached input tokens at the row's cached rate. With no model set, the model Codex actually used is read from its session file. A model in neither table leaves `cost_usd = 0` and flags the run `cost_unknown = true`. |

### Editable pricing table

The `codex_cli` cost estimate is driven by a per-model USD-per-1M-token
price, stored in the DB-backed `model_pricing` table and editable from
**Settings → Pricing** in the UI (or directly via `GET`/`PUT
/api/v1/settings/pricing`). The table is seeded on first migration from the
same values that used to be hardcoded, so estimates are unchanged until you
edit a row; edits take effect on the very next run completion without a
restart. A model not listed in `model_pricing` falls back to a small,
approximate hardcoded map in `internal/agent/providers/pricing.go` (exact
match, then longest model-ID-prefix match); a model matching neither has
`agent_runs.cost_unknown` set to `1` for that run (surfaced in the run
history UI as "cost unknown") instead of silently showing `$0`, so a stale
or missing price is visible rather than mistaken for a genuinely free run.
`claude`/`opencode` never set `cost_unknown` — their CLI-reported cost
(including a legitimate `$0` under a subscription plan) is always
authoritative. `codex_cli` and `qwen_code` both price their captured tokens
through this table and can set `cost_unknown` for an unpriced model.

Each row can also carry an optional **cached input** price
(`cached_input_per_1m`). `codex_cli` reports how many of a run's input tokens
were prompt-cache hits, and long agent runs are mostly cache hits, so billing
them at the full input price overstates cost several times over. `qwen_code`
reports the same thing (`cache_read_input_tokens` on its result usage) and is
priced the same way. A row with no cached price bills cached tokens at its
input price, which is how every row was priced before the column existed.
The seeded OpenAI rows ship with their published cached rates. Current Codex
models (for example the `gpt-5.6`/`gpt-6.x` families Codex defaults to) and
Qwen's own models are not in the seeded table, so add a row for the model you
use to get an estimate.

The Dashboard shows an aggregate total (tokens + cost) across all runs in a terminal state (`completed`/`failed`/`waiting_human`), plus a per-provider breakdown (via `provider_configs.provider`, joined `agent_runs.agent_config_id` → `agent_configs` → `agent_configs.provider_config_id` → `provider_configs`). The aggregate total query does not join on `agent_configs`, so it includes every terminal run regardless of its config. The per-provider breakdown *does* join on `agent_configs`/`provider_configs`, so runs whose agent config was later deleted (`agent_config_id` is set `NULL` on delete) are excluded from that breakdown, since they can no longer be attributed to a provider — a known limitation.

The Dashboard also breaks cost/usage down further into a **per-agent-config
performance table**: success rate (completed/failed/waiting_human counts),
average and p90 run duration, average "runs per task" (how many `agent_runs`
rows a done task needed under this config — a dispatch/retry-cycle count,
*not* the internal LLM turns within a single run that `max_turns` caps),
average and p90 *turns actually used* per run alongside the config's current
`max_turns` cap (so an overly generous or too-tight cap is visible at a
glance — averaged only over runs that reported a count; `codex_cli` and
`opencode` report their completed tool steps / `step_finish` events as a
proxy for turns, so only a run that ended before completing a single step
is excluded as "not reported" rather than counted as zero), a
transient-retry snapshot, and tokens/cost — all grouped by `agent_config_id`
instead of just `provider`, so you can compare individual agent configs
against each other (e.g. "is opus-on-review worth it?") instead of only
comparing at the provider level. Same terminal-state + still-existing
`agent_config_id` filtering as the per-provider breakdown above (a run whose
config was later deleted can't be attributed to any config, per-provider or
per-config). Two known limitations apply here as well:

- **Proportional split vs. last-run attribution.** "Runs per task" is a
  proportional split: each done task contributes 1.0 "task credit" divided
  across every agent config that ran on it, weighted by that config's share
  of the task's total runs. A task retried twice under agent A and then
  finished by agent B in one run gives A 2/3 of a task credit (and 2 runs)
  and B 1/3 of a task credit (and 1 run) — so a config that only ever hands
  work off to another config (e.g. a Worker whose tasks always finish under
  a Reviewer) still shows up here. The retry snapshot, by contrast, is still
  attributed entirely to the task's **last** run's agent config.
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
`input_tokens`/`output_tokens` via the same pricing table, plus the model's
optional `cached_input_per_1m`/`cache_write_input_per_1m` rates (Settings →
Pricing) for the `cache_read_input_tokens`/`cache_creation_input_tokens`
counters Claude/qwen report *additional to* `input_tokens` (not a subset of
it, unlike `codex_cli`'s single cached-tokens counter — see
`extractAssistantUsage` in `parse_streamjson.go`). Cache reads are typically
far cheaper than a full input token and cache writes somewhat more
expensive; pricing them both at the plain input rate (the old behavior)
over-projected cost by several times on a typical cache-heavy run and could
kill it well before the real budget. A model row with no cache rates set
still bills cache tokens at its plain input rate, same as before this
existed. Two consequences:

- **Subscription plans.** Under a Claude subscription where the real
  marginal cost of a run is `$0`, the pricing-table estimate can still be
  nonzero and can still trigger a mid-run kill. If you rely on a cost budget
  under a subscription, size `max_cost_usd` generously (or leave it unset)
  rather than treating it as a precise dollar cap.
- **Unpriced models.** If the configured model isn't in the pricing table,
  the watchdog can't project a cost at all and is a silent no-op — the
  pre-dispatch guard is the only enforcement for that run (and it, too, may
  under-report if the model's cost was never priced on prior runs).

Providers without mid-run priced usage — `codex_cli` and `opencode` — do **not** support the mid-run kill
switch (`codex_cli` prices its captured tokens via the pricing
table — see [Cost & Usage
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

## Effort

`effort` is an optional per-agent-config reasoning effort level: `""`
(default/unset — leaves the provider's own default in place), `low`,
`medium`, `high`, `xhigh`, or `max`. It maps onto each provider's own
reasoning-effort surface differently (see the [Capability
Matrix](#capability-matrix) above):

- **`claude` — full support.** Verified against a live `claude` CLI (v2.1.223):
  there is a first-class `--effort <level>` flag accepting exactly
  `low|medium|high|xhigh|max` (no `off`/`minimal` tier — there is no way to
  force zero reasoning via this flag). Set from `buildClaudeArgs`
  alongside `--model`/`--resume`.

  **Silent-degradation risk.** An unrecognized `--effort` value does **not**
  make the CLI exit non-zero — it prints `Warning: Unknown --effort value
  '<v>' — ignoring it and using the default effort.` to stderr and
  continues at the provider default. Backend-side validation (`POST`/`PUT
  /api/v1/agents` reject anything outside `"", low, medium, high, xhigh,
  max` with a 400) is the only thing that actually prevents a typo'd or
  stale value from reaching a live run — the CLI will not catch it for you.
  Two further ways an accepted value can still be a silent no-op, entirely
  outside this codebase's control: not every model supports effort levels,
  and an Anthropic organization can restrict which levels are available to
  it. The runner logs `requested effort=<level>` as a system log line for
  every run where one was set, so you can cross-reference what was actually
  requested against the run's own log output (and the CLI's stderr warning,
  if any) when the effect looks wrong.

- **`codex_cli` — partial support (clamped).** Codex has no `--effort`
  flag; reasoning effort is set via a `-c
  model_reasoning_effort="<level>"` config override on `codex exec`, which
  accepts exactly `minimal|low|medium|high`. Since this codebase's `effort`
  enum has no `off`/`minimal` tier, `xhigh` and `max` (levels codex has no
  equivalent tier for) are clamped down to `high` rather than passed
  through unrecognized.

- **`qwen_code` — not supported.** No reasoning-effort flag or config knob
  on the qwen CLI; the field is simply ignored.

- **`opencode` — partial support (pass-through).** Mapped to `opencode run
  --variant <value>`, passed through unchanged for `low|medium|high|xhigh|max`
  (no clamping, unlike `codex_cli`) and omitted entirely when unset. Unlike
  `claude`/`codex_cli`, there is no fixed vendor enum to validate or clamp
  against: `--variant` is forwarded by opencode straight to whichever model
  provider it's configured against, and the variants an individual model
  actually understands are model-specific. An accepted-by-us value can
  still be a **silent no-op or an outright rejection** further upstream,
  entirely outside this codebase's control. The runner logs `requested
  effort=<value> (--variant)` as a system log line for every run where one
  was set, so you can cross-reference it against the run's actual behavior.
  See [providers/opencode.md § Effort](providers/opencode.md#effort).

**Cost interaction.** Raising effort meaningfully increases token spend
(more reasoning tokens per turn) — see [Cost & Usage
Tracking](#cost--usage-tracking) and [Cost Budgets](#cost-budgets) above.
An agent config with both a high `effort` and a tight `max_cost_usd` may
trip the mid-run cost watchdog (or the pre-dispatch budget guard) sooner
than expected; consider raising the budget alongside the effort level, or
leaving `effort` unset for cost-sensitive configs.

## Permission Mode

`permission_mode` is an optional per-agent-config Claude CLI permission
mode: `""` (default/unset), `default`, `auto`, `acceptEdits`, or
`bypassPermissions`. Claude-provider only — other providers ignore it (see
the [Capability Matrix](#capability-matrix) above; `codex_cli` and
`qwen_code` always run in their own always-bypass modes regardless of this
field).

**Why this exists.** Headless `claude` task runs previously passed no
`--permission-mode` at all, so the CLI fell back to its own default —
`auto`, a cloud safety classifier that can transiently deny harmless
commands (e.g. `gofmt`) with `Classifier unavailable`. `permission_mode`
makes that choice an explicit, per-agent-config setting instead of an
implicit CLI default.

- **`""` (unset) — today's behavior, unchanged.** No `--permission-mode`
  flag is passed; byte-identical to every spawn before this field existed.
  The CLI's own default (`auto`) applies.
- **`default`** — every tool call requires interactive approval. Not
  practical for headless runs (no human present to approve) unless
  something else in the pipeline auto-approves.
- **`auto`** — explicitly selects the same cloud safety classifier as the
  CLI's own default. Can transiently deny safe commands when the
  classifier is unavailable.
- **`acceptEdits`** — auto-approves file edits (Edit/Write) but still gates
  other tool calls (e.g. Bash) behind approval.
- **`bypassPermissions`** — skips approval prompts entirely for this
  agent's runs.

**Deny rules still apply under `bypassPermissions`.** Verified empirically
against a live `claude` binary (v2.1.238): a `command_denylist` entry
(translated to `permissions.deny`, see [Command Allowlist /
Denylist](#command-allowlist--denylist) below) still refused a matching
Bash command (`permission_denied` event, populated `permission_denials`)
even with `--permission-mode bypassPermissions` set. `bypassPermissions`
only skips the interactive *approval* prompt — it does not override an
explicit deny rule. See `docs/providers/claude.md` for the spawn-site code
comment recording this finding.

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
<feedback from the previous run, if any; only present until a run that received it succeeds>

---]
[HUMAN DECISIONS ON THIS TASK:
<every human reply given on this task, with question and date>

---]

[REPO LESSONS (conventions learned from past tasks in this repo; if one contradicts the code, trust the code and say so in your notes):
- [kind] <lesson body>
...

---]
(active repo + global lessons, filtered by stage/path globs, max 15 / ~2,000 chars; see [lessons.md](lessons.md). Not sent on resumed sessions.)


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
- **Scope:** this is currently `claude`-provider-only. Other providers (`opencode`, `qwen_code`, `codex_cli`) have the same DB columns available but ignore them entirely.

## Command Allowlist / Denylist

Every agent config has an optional `command_allowlist` and `command_denylist` — JSON
arrays of shell-command glob patterns (`"*"` wildcard), both defaulting to `[]` (no
restriction, i.e. today's existing behavior for all pre-existing configs). These let
you limit which shell commands a given agent config's Bash/`run_bash` tool may run,
reducing the blast radius of a misbehaving or prompt-injected agent — **this is
best-effort string matching, not a sandbox.** It does not prevent an agent from
constructing a denied command indirectly (via `$()`, backticks, string
concatenation, base64-decoded payloads, etc).

> **These lists are guardrails, not a sandbox.** They match command prefixes, so an
> agent can get around them with `sh -c '...'`, `env`, an interpreter, or a script
> file. `Bash` is always allowed for the `claude` provider, and `codex_cli` and
> `qwen_code` run with approvals and sandboxing off. Don't rely on these lists to
> contain an untrusted or prompt-injected agent; run such agents only against
> repos and credentials you are prepared to expose.

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
| `claude` | Enforced natively by the `claude` CLI via `permissions.allow`/`permissions.deny` in the `--settings` JSON (same `Bash(pattern)` syntax as `--allowedTools`/`--disallowedTools`). Both allowlist and denylist supported; smoke-tested against a live `claude` binary. |
| `qwen_code` | `command_allowlist` is **not enforced** — qwen's `--allowed-tools` only bypasses its confirmation prompt (it doesn't restrict anything), and this runner always passes `--approval-mode yolo` (auto-approve all tools) on top, making an allowlist moot either way. `command_denylist` **is enforced** via `--exclude-tools run_shell_command(pattern)` entries (qwen's shell tool is named `run_shell_command`, not `Bash` — a `Bash(pattern)` entry would be a silent no-op here), which qwen folds into its `permissionsDeny` policy and honors even under `yolo`. qwen's own matching is a literal exact-or-prefix comparison, not a true glob — a trailing `*` in the pattern (this codebase's shared glob convention) is normalized to a prefix before being sent, but a `*` anywhere else in the pattern (e.g. the suffix-matching `"* --force"` form documented above) has no qwen-side equivalent and won't match. See [providers/qwen_code.md](providers/qwen_code.md). |
| `opencode` | `command_denylist` entries map to `"deny"` and `command_allowlist` entries (if set) map to `"allow"` plus a catch-all `"*": "deny"` default, in a `permission.bash` block delivered via `OPENCODE_CONFIG_CONTENT` (opencode has no `--mcp-config`-style CLI flag for this). `opencode run` auto-rejects "ask" permissions headless since `--auto` is deliberately never passed, so a "deny" entry genuinely blocks the pattern. Rules are emitted `*` → allow → deny, matching opencode's documented last-match-wins evaluation ([docs](https://opencode.ai/docs/permissions/)), so a deny beats an overlapping allow. See [providers/opencode.md](providers/opencode.md). |
| `codex_cli` | **Not enforced at all** — Codex has its own native sandbox/approval-mode system instead (`--sandbox`, `--ask-for-approval`), bypassed entirely by the `--dangerously-bypass-approvals-and-sandbox` flag this provider must pass for headless operation. See [providers/codex_cli.md](providers/codex_cli.md). |

See the corresponding [provider docs](#providers) for details on each provider's mechanism.

## Retry Policy

Not every `failed` run means the agent's work actually failed — sometimes
it's a transient infrastructure hiccup (an API rate limit, a network blip, an
upstream `5xx`, an ambiguous timeout). The retry policy distinguishes the two
so humans aren't paged for problems that will resolve themselves:

- **Genuine failure** — the agent ran and the work itself failed (e.g. tests
  didn't pass, the agent gave up). Behaves as before: the task stays on its
  current label and the next dispatcher sweep (~5s) re-picks it up. After
  **3 consecutive** genuine failures the task is escalated to `waiting_human`
  (lock kept) instead of being re-run again; a successful label
  transition, a reply, or a re-run resets the streak. A run that completes but
  whose requested transition is rejected by the workflow (WIP limit, guard, no
  such transition) counts toward the same streak: below the limit the lock is
  released so it can retry, and at the limit the run becomes `waiting_human`
  with a note such as `Agent asked to move to "review" but the move was
  rejected: ...`. A concurrent human move of the task is not counted.
- **Low-memory stop** — when `memguard` cancels a run for low container memory the run ends `cancelled` (not a failure, task not paused), and the task gets a cooldown in `next_retry_at` (60s, doubling on back-to-back stops of the same task, capped at 5 minutes; `transient_retry_count` stays 0). It shows as the `retry_backoff` block reason / "Cooling down" badge. Three such stops within 3 minutes across any tasks also pause new sweep dispatch for 60s (in-memory; human-reply dispatch is not gated).
- **Transient failure** — a network-level error,
  upstream `5xx`, or (for CLI providers) a text match for signals like
  connection resets or `502/503/504`. The match is anchored (word/HTTP-status
  boundaries, not a bare substring, so e.g. a diff hunk header containing
  `429` doesn't false-positive) and, on stdout, is only applied to output
  that isn't a structured event — a successfully-parsed event has already
  been classified via its own typed path; stderr is always matched, since
  it's untyped diagnostic output. These are auto-retried up to `max_retries`
  times with exponential backoff
  (`retry_backoff_secs * 2^attempt`, capped at 10 minutes) before the task is
  escalated to `waiting_human` — so a human only gets involved once the
  automatic retries have been exhausted, not on every blip.
- The task board shows a **"↻ Retrying (n)"** badge while a task is in a
  backed-off auto-retry window, so it's clear at a glance that no action is
  needed yet.
- Setting `max_retries` to `0` disables auto-retry for that config entirely:
  the first transient failure escalates straight to `waiting_human` (there is
  no silent immediate re-dispatch).
- A run that **completes without reporting an outcome** (no `signal_complete`,
  no `OUTCOME:` line) is escalated to `waiting_human` once, with a note on the
  run, rather than being re-run every sweep.
- **Rate limit** — a `429` or a provider usage/session limit. This blocks
  the *provider account* (usage pool) from new dispatches until it resets,
  because the limit belongs to the account, not the task. When the provider
  says when that is (Claude's "resets 6pm" message) the block lasts until
  then. When it doesn't (opencode, Qwen Code, API-key accounts), a
  background **reset check** sends the account's CLI a one-line "reply OK"
  prompt, outside any task and with no MCP tools, 1, 2, 5, 10 and 15 minutes
  after the limit and then every 15 minutes, and unblocks the account as
  soon as one gets through (each check is logged under
  `component=reset_check`). These probes are not recorded as runs or cost. Every
  agent config on that account is skipped, not only the one whose run hit
  the limit. Each provider config is its own account, except that `claude`
  configs on the shared `claude login` (no `ANTHROPIC_API_KEY`,
  `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_BASE_URL`,
  Bedrock or Vertex in their env) share one, as do `codex_cli` configs on the
  shared ChatGPT login (no `OPENAI_API_KEY`, `CODEX_API_KEY` or
  `OPENAI_BASE_URL`); a `CLAUDE_CONFIG_DIR` / `CODEX_HOME` makes a separate
  login. Runs already in flight on that account when the limit lands still
  finish (or fail) on their own. It does **not**
  spend the task's retry budget: the task is released right away, so the next
  sweep hands it to the next provider in the agent config's
  [provider list](#provider-list), then to the next enabled config for the
  same label (by `priority`), or, when every matching config is blocked, leaves it waiting
  with the "all matching configs rate-limited" block reason until a block
  expires. Rate limits therefore never escalate a task to `waiting_human`,
  whatever `max_retries` is set to. The per-account block and its consecutive-429 backoff counter are persisted (`agent_rate_limits` table) and reloaded at startup, so a restart or deploy does not immediately re-hit a provider that just rate-limited; expired rows are pruned on boot, and a database error only logs without blocking dispatch.
- **Turn-limit exhaustion** — the agent hit its configured `max_turns` cap
  without finishing (`claude`, `qwen_code`; see the
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
`env` field above. Backend-only secrets like the API's own `API_TOKEN` are
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

**`env` values are write-only over the HTTP API.** `GET`/list responses for
provider configs (`/api/v1/provider-configs`), and every place a provider
config is embedded in another response (an agent config's `provider_config`,
a chat session's `provider_config`), mask every `env` value as the literal
string `"***"` — key names are preserved (useful for the UI to show what's
configured), but real values never leave the server over these endpoints.
Writing (`POST`/`PUT`) treats `"***"` as "keep the value already stored for
that key," any other value (including `""`) as an explicit overwrite, and a
key present in the stored env but omitted from the request's `env` object as
a delete; omitting `env` from the request body entirely leaves the whole
thing unchanged. This lets the Providers page round-trip a config (fetch,
tweak one field, save) without ever having the real secret pass through the
browser twice, and without accidentally wiping a secret it never displayed.

This is **response redaction only, not encryption at rest.** `env` values are
still stored in cleartext in the SQLite database file, and `GET
/api/v1/backup` — a raw point-in-time snapshot of that database — still
contains them in the clear by design (see [docs/backup.md](backup.md)). The
DB file and any backups of it still need to be protected as if they held
plaintext credentials, because they do.
