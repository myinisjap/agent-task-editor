# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

To cut a release, run the "Prepare Release" workflow manually from the Actions
tab and pick a version bump (patch/minor/major) — it moves this file's
`[Unreleased]` section under a new version heading, commits that to main, then
tags it. That tag push triggers the separate "Release" workflow, which builds
and pushes the images and creates a GitHub Release using that section as the
release notes. The `[Unreleased]` section must have content or "Prepare
Release" fails.

Alternatively, for hotfixes where you want to hand-edit this file yourself,
add a `## [x.y.z] - YYYY-MM-DD` section below with the changes and push the
matching tag directly (`git tag vx.y.z && git push origin vx.y.z`), which
triggers the "Release" workflow the same way.

## [Unreleased]

### Added
- **Mid-run cost kill switch + budget early warning.** `max_cost_usd`
  budgets previously only gated the *next* dispatch, so a single runaway run
  could blow arbitrarily far past its cap before anything noticed. Providers
  with mid-run priced usage (`claude`, and `qwen_code` when its configured
  model is priced) now watch incremental token usage as a run streams,
  project total cost via the pricing table, and cancel the in-flight
  subprocess the moment projected cost crosses the effective budget,
  escalating to `waiting_human` instead of a plain failure (`codex_cli`,
  `opencode` remain unsupported and are documented as such — see the
  provider capability matrix). A killed run's own token usage/cost is
  persisted from the watchdog's cumulative-usage snapshot (never left at
  `$0`), so the task's recorded spend stays accurate across repeated
  kill/resume cycles. There's also a new configurable early-warning
  threshold (default 80%, `GET`/`PUT /api/v1/settings/cost-warning`) that
  fires a `task.cost_warning` WebSocket event ahead of the hard cap, shown as
  a badge on the board and a banner on Task Detail. See [docs/agents.md §
  Cost Budgets](docs/agents.md#cost-budgets).
- **Per-label WIP limits.** Labels can now carry an optional `wip_limit`
  (nullable — `null`/unset means unlimited, unchanged from before this
  feature). The board column header shows `count / limit` and flags the
  column visually once its count reaches or exceeds the limit. Enforcement is soft by default
  (visual only); an opt-in `wip_limit_hard` flag makes the dispatcher apply
  backpressure instead — it skips picking up a task whose agent "success"
  transition targets a label already at or over its limit, so work queues
  upstream on its current label rather than erroring mid-run or piling into
  an already-full column. Configurable via the workflow YAML (`wip_limit`,
  `wip_limit_hard` on a label) or the JSON workflow API. See
  `docs/workflows.md`.
- **Merge-conflict detection on open PRs.** The GitHub PR status sweep now
  also asks GitHub whether each task's open PR still merges cleanly into its
  base branch, so a PR that goes stale because someone else merged first is
  noticed without anyone opening GitHub. The verdict is stored on the task as
  `pr_mergeable` (`mergeable` / `conflicting` / `unknown`), pushed to the UI as
  a new `task.pr_mergeable_changed` WebSocket event, rendered as a conflict
  marker on the board card and task detail header, and refreshed on demand by
  `GET /tasks/{id}/github-status`. When a conflict first appears, the sweep
  appends a "resolve the conflict" note to the task's current agent run
  feedback (and, for repos with `pr_review_auto_transition_enabled`, moves the
  task back along its workflow's failure path) so an agent picks the work back
  up. The check rides along with the existing PR head-SHA lookup, so it costs
  no additional `gh` call per sweep.
- **Per-repo concurrency limits.** Repos can now set an optional
  `max_concurrent_runs` cap (Repos page) on how many agent runs the
  dispatcher will keep in flight against that repo at once, independent of
  the global `MAX_WORKERS` pool size. Previously one repo with many eligible
  tasks (e.g. from a schedule or a bulk GitHub Issues import) could occupy
  every worker slot and starve every other repo indefinitely. Leaving the
  cap unset preserves prior behavior exactly (falls back to the global
  limit). The Dashboard's new "Repo concurrency" section shows live in-use
  vs. effective-limit worker slots per repo.
- **First-run onboarding checklist on the Board** that sequences setup steps
  (add a repo → configure a provider → create an agent config → create your
  first task), checks each off live as configuration lands, folds in
  `/health/providers` readiness checks so a failing credential check (e.g.
  Claude CLI not authenticated) surfaces against the relevant step, and stays
  dismissed permanently once dismissed or once all steps pass (#258).
- **Imported GitHub issues now stay in sync with their board tasks.** The
  importer was create-only: once a task existed for an issue, nothing about
  that issue was ever looked at again, so an edited title or body never
  propagated, a `bug` → `chore` relabel never updated the task's type, and a
  closed issue left its task on the board indefinitely. Each sweep now also
  updates drifted fields and reconciles issues that have disappeared from the
  fetch. Configured per repo:
  - `issue_sync_update_policy` (`gate` | `always` | `never`, default `gate`) —
    when upstream changes are applied. The default applies them only while the
    task is still on the workflow's human-gate label, so an issue can be
    refined before work starts, and freezes the task once a human or agent
    moves it.
  - `issue_sync_gone_action` (`flag` | `archive` | `move`, default `flag`) and
    `issue_sync_gone_label` — what happens when an issue is closed or loses the
    sync filter label. The default records it and takes no workflow action; the
    task detail page shows a warning badge instead of the task silently
    looking like any other. A task with a running agent is only ever flagged,
    never archived or moved, and the flag clears automatically if the issue
    reopens.
- **Issue comment ingestion (opt-in, `issue_comment_sync_enabled`).** The
  comment thread on a task's source issue is read onto the task and rendered
  into the agent's prompt. This closes the last feedback channel nothing read:
  PR review comments already reached agents, but the pre-work conversation on
  the issue — where the cheapest course correction lives — did not. Off by
  default, and limited to comments from authors with write access to the repo,
  because it widens the prompt-injection surface: previously an attacker had to
  file or edit an issue matching the sync filter, whereas anyone who can
  comment on a synced issue would otherwise get text in front of an agent. The
  prompt section marks its contents as data rather than instructions and fences
  them so a comment cannot forge the delimiter and escape into trusted context.
  Comments this system posted itself are filtered out, so an agent never reads
  its own "PR opened" notice back as human input.
- `GET /tasks/{id}/source-comments` returns a task's ingested issue thread.
- **Duplicate an existing task.** A "⧉ Duplicate" action on the task detail
  page and on each board card opens the New Task modal pre-filled with the
  source task's title (suffixed `(copy)`), description, type, priority, repo,
  and workflow — editable before creation, same as any new task. The clone
  starts clean: no run history, label history, diff comments, PR link,
  worktree/git state, subtasks, or attachments carry over, and it lands on the
  workflow's starting label like any other new task. The source task is never
  modified. No new endpoint was needed — the existing `POST /tasks` create
  path already covers every field involved.
- **Archiving a task now reclaims its worktree, and a new sweeper catches
  everything else.** Archiving is the documented way to retire a dead or
  abandoned task, and such a task is typically on a non-terminal label — none
  of the existing teardown paths (reaching a terminal label, task delete,
  ghsync's post-merge cleanup) ever fired for it, and archived tasks are
  excluded from every sweep that might otherwise revisit them, so its
  `.ate-worktrees/<id>` directory persisted forever. `PATCH /tasks/{id}/archive`
  and the bulk `archive` action now tear down the worktree immediately,
  best-effort (branch kept for review, same as every other teardown path). A
  new always-on sweeper (`WORKTREE_SWEEP_INTERVAL`, default `10m`) also
  periodically reconciles every repo's `.ate-worktrees/*` against live
  (non-archived) task/chat-session ids and reclaims anything else, so disk
  usage under `.ate-worktrees/` is bounded by live tasks rather than by every
  task ever created — this also catches a worktree orphaned by a crash.
  Archiving also clears the task's `worktree_path`, and `ensureWorktree` now
  verifies its recorded worktree still exists before reusing it — otherwise
  unarchiving a task (or a run reprovisioned after the sweeper reclaimed its
  worktree) would hand the next agent run a cwd that no longer exists rather
  than reprovisioning a fresh one. See
  [docs/backup.md#orphaned-worktree-sweeper](docs/backup.md#orphaned-worktree-sweeper).
- **Chat terminal sessions can now be capped and idle-reaped.** Each Chat-tab
  session keeps a live CLI subprocess (plus a scrollback buffer) running
  indefinitely across WebSocket disconnects, with nothing previously bounding
  how many accumulate or how long an unattached one stays alive. New opt-in
  settings `CHAT_MAX_SESSIONS` (refuses starting *new* sessions past the cap;
  reattaching to an existing session is never blocked) and
  `CHAT_IDLE_TIMEOUT` (reaps a session's subprocess after it's gone unattached
  for this long) — both default to off/unlimited, so behavior is unchanged
  unless explicitly configured. See
  [docs/getting-started.md#chat-terminal-sessions](docs/getting-started.md#chat-terminal-sessions).
- **Per-repo configurable issue write-back label.** The label applied to a
  task's source GitHub issue when it first leaves the workflow's human-gate
  label was previously a fixed `agent-in-progress`. It's now configurable via
  `repos.issue_writeback_label` (the "In-progress label" field on the Repos
  page, under "Issue write-back"); leaving it blank preserves the previous
  `agent-in-progress` default. The label — default or custom — still must
  already exist on the GitHub repo, or the write-back silently no-ops.
- **Provider capability gaps surfaced inline at config time.** `AgentConfigForm`
  and `ProviderConfigForm` now flag unsupported options for the selected
  provider (MCP servers/plugins, subtasks, session resume, cost tracking,
  label transitions, command allow/denylist enforcement) instead of silently
  hiding controls or letting a misconfigured agent fail at run time. The
  capability data lives in `frontend/src/lib/providerCapabilities.ts`, the
  single source of truth also used to keep `docs/agents.md`'s capability
  matrix in sync.
- **`GET /tasks/{id}/runs` and the config list endpoints are now cursor-
  paginated**, extending the pattern already used by `GET /tasks` and run
  logs. `GET /tasks/{id}/runs` (default limit 100, cap 500) is the one that
  matters most — a long-lived task with retries/reruns can accumulate runs
  indefinitely, and every task-detail load used to fetch every one of them.
  `GET /provider-configs`, `GET /agents`, `GET /repos`, and `GET /workflows`
  (all default limit 200, cap 500) get the same treatment for consistency.
  Each accepts `?limit=`/`?after=` and returns the next-page cursor in an
  `X-Next-Cursor` header, mirroring `GET /tasks`. Since a task's lifetime
  cost is no longer derivable by summing a single page of runs, `GET
  /tasks/{id}` now also returns `cumulative_cost_usd` (computed server-side
  across every run, any status) and the task-detail cost badge reads it from
  there instead of summing the fetched runs client-side. The frontend's
  `api.workflows.list()`/`api.agents.list()`/`api.providerConfigs.list()`/
  `api.repos.list()` still resolve to a plain array — the client transparently
  pages through `X-Next-Cursor` and concatenates — so no dropdown/store
  callers needed to change.
- **Every `/api/v1/*` error response is now JSON with a consistent shape.**
  A handful of paths — the chat/terminal WebSocket upgrade's pre-upgrade
  checks (`internal/api/handlers/chat.go`), the bearer-auth 401
  (`middleware/auth.go`), and the main WebSocket endpoint's pre-upgrade auth
  rejection (`internal/ws/client.go`) — previously returned plain-text
  `http.Error` bodies, forcing clients to special-case exactly the paths
  where clean error handling matters most (a misconfigured token). They now
  all emit `{"error": "..."}` like every other handler. A genuine WebSocket
  protocol-level upgrade failure (inside `websocket.Accept` itself, after
  auth has already passed) is unchanged — there's no HTTP response left to
  shape at that point, so it's just logged.
- **`GET /readyz` readiness probe.** Unlike `/healthz` (a static liveness
  stub), `/readyz` pings the database and checks that the dispatch loop has
  ticked recently, returning `503` if either check fails. The Docker Compose
  healthcheck for the `backend` service now targets `/readyz` instead of
  `/healthz`, so a backend with a locked SQLite file or a wedged dispatch
  loop is reported unhealthy instead of appearing healthy forever. See
  [docs/api.md](docs/api.md#get-readyz).
- **Backend restart policy and default memory ceiling.** The `backend`
  service in `docker-compose.yml` / `docker-compose.release.yml` now has
  `restart: unless-stopped` (previously only `frontend` restarted
  automatically) and a default 2 GB memory limit via
  `deploy.resources.limits.memory`, so a crashed or OOM-killed backend comes
  back on its own instead of staying down until an operator notices, and a
  runaway agent run can no longer consume unbounded host memory. See
  [docs/getting-started.md](docs/getting-started.md#backend-resilience-restart-policy-readiness-and-memory-limit).
- **Closed structural test gaps across the WS layer, provider subprocess
  lifecycle, and CI coverage gating** (#251). Previously, every "WS event →
  UI state" path in the frontend was unverified because tests mocked
  `wsClient.on` as a permanent no-op — exactly how #249's dead running-
  indicator regression survived; `TaskDetailPage.test.tsx` and a new
  `api/ws.test.ts` now capture the real handler(s) and drive it with
  simulated events (reconnect backoff, ticket-refresh-on-401,
  resubscribe-on-reopen, malformed-message handling). On the backend, the
  `claude_test.go` fake-binary re-exec harness (previously claude-only) is
  now generalized to `codex`/`gemini`/`qwen`/`opencode`, adding real `Run`/
  `binary`/`prepareXHome`/`Cleanup` lifecycle coverage plus a shared
  context-cancel/timeout-kill test per provider — the prerequisite for the
  #243 regression test. Also added: `internal/ws.ServeWS` end-to-end tests
  (ticket vs. deprecated `?token=` auth, origin CORS, the 100-subscription
  cap), `providers.mergeEnv`'s `dangerousEnvKeys` blocklist,
  `extractOutcome`, `dispatcher.copyAttachmentsToWorktree`, `Terminal` WS
  upgrade auth, and upload MIME-sniffing/path-safety checks. CI now fails a
  PR if total statement coverage drops below a floor (55% backend and
  frontend, a few points under the ~58.8%/~59.75% measured when this gate
  was added, to absorb normal `-race` run-to-run jitter) instead of only
  reporting coverage with no gate.
- **Broader E2E coverage.** The Playwright smoke suite now asserts every
  app route (dashboard, board, chat, workflow, and all configuration pages)
  loads correctly, and runs the whole suite twice — once on a desktop
  viewport and once on a mobile viewport — via a new `mobile-chrome`
  Playwright project, so mobile-only layouts (collapsed nav, mobile header
  bars) get smoke coverage too. See `frontend/e2e/README.md`.

### Removed
- **The `gemini_cli` provider has been removed.** The Gemini CLI is no longer
  supported upstream in its previous form; Google's replacement CLI
  (Antigravity) will be considered as a new provider in a separate future
  issue, out of scope here. `GeminiRunner`, `classifyGeminiJSON`, the Gemini
  health check, the `gemini_cli` entries in the provider dropdown/capability
  matrix/OpenAPI enum, the Docker `INSTALL_GEMINI_CLI` build arg, and every
  `gemini_cli`/Gemini mention across the docs have been deleted. Provider or
  agent configs still pointing at `gemini_cli` will no longer dispatch — new
  runs against them fail immediately with an "unknown provider" error instead
  of launching the CLI — so switch any such configs to another provider
  before upgrading.

### Fixed
- **Subtask creation from planning runs no longer silently fails on permission.**
  When an agent config had `subtasks_enabled`, the sidecar registered the
  `create_subtask` MCP tool, but the Claude and Qwen runners never added it to
  their explicit tool allow-lists (`--allowedTools` / `--allowed-tools`). The CLI
  offered the tool, the agent called it, and the call was rejected with "Claude
  requested permissions to use mcp__task-editor__create_subtask, but you haven't
  granted it yet" — so planned subtasks were dropped and the agent could only
  note the failure. Both runners now allow-list `create_subtask` whenever the
  config opts in. (Gemini/Codex were unaffected — they blanket-approve tools.)
- **`codex_cli` runs now estimate cost from token usage, so `max_cost_usd`
  budgets are actually enforced for codex tasks** (#245). `classifyCodexJSON`
  captured `usage.input_tokens`/`usage.output_tokens` off the `turn.completed`
  event, but `CodexRunner.Run` never priced them — every codex run persisted
  `cost_usd = 0`, so the dispatcher's pre-dispatch cost-budget guard
  (`checkCostBudget`/`SumTaskCost`) saw permanent zero spend and `max_cost_usd`
  never tripped for a codex-provider task, no matter how much was actually
  spent. Codex now prices its captured tokens through the same estimation
  path as `anthropic`/`llm`/`qwen_code` (`applyUsageWithCost`, the DB-backed
  `model_pricing` table with a hardcoded fallback), on every path that
  persists usage — including the truncated, timed-out, rate-limited,
  transient-error, and failed-with-no-outcome returns, not just the golden
  path, since a run can spend money before crashing. A run against a model
  with no resolvable price now sets `cost_unknown = true` (mirroring
  anthropic/llm) instead of silently recording `$0`, so the UI shows "cost
  unknown" rather than implying a genuinely free run. Additionally, the
  dispatcher's pre-dispatch budget guard now treats an under-budget task with
  at least one cost-unknown run as unable to trust its own accumulated
  spend: it escalates to `waiting_human` with a
  `cost budget cannot be enforced: N run(s) have unknown cost` message
  pointing at Configuration → Pricing, rather than letting an unpriced
  model's runs quietly count as free and let the task dispatch unbounded
  against a budget that no longer means anything (this only fires while the
  task is otherwise under budget — an independently-exhausted budget still
  reports the ordinary "budget exhausted" message). Note: the upstream issue
  also named `gemini`/`opencode` — this repo has no `gemini` provider, and
  `opencode` already records authoritative cost/tokens directly from the CLI
  (fixed separately in #287/#304), so this fix is `codex_cli`-specific. See
  [docs/providers/codex_cli.md § Cost & Usage
  Reporting](docs/providers/codex_cli.md#cost--usage-reporting) and
  [docs/agents.md § Cost Budgets](docs/agents.md#cost-budgets).
- **Board "agent running" indicator now works.** The pulsing dot on a task
  card never rendered for any task: `BoardPage`'s `runningTaskIds` state had
  no setter and was never populated, even though the `task.agent_started` /
  `task.agent_done` WS events it needed were already being handled in the
  same effect for other purposes. The dot now activates on
  `task.agent_started`, clears on `task.agent_done` (success or failure), is
  seeded on load (and on every task upsert) from a task's
  `active_agent_run_id` so a mid-run page refresh still shows it, and is
  cleared whenever the WebSocket connection drops so a missed `agent_done`
  can't leave it stuck — it re-seeds correctly once tasks are refetched
  after reconnect. Also fixed: moving a task off its running label — a
  drag-and-drop, an Approve/Reject, or any other human-triggered transition
  — goes through `workflow.Engine.Transition`, which clears
  `active_agent_run_id` server-side but only publishes `task.label_changed`,
  never `task.agent_done`; the indicator now has an explicit
  `task.label_changed` handler so it clears immediately on any label move
  instead of staying stuck until an unrelated `agent_done` or a WS
  reconnect happened to clean it up.
- **The agent config form now warns when `max_turns` is set on a provider
  that doesn't enforce it.** `codex_cli` and `opencode` have no turn-cap
  flag, so a run on those providers is only bounded by `timeout_secs`
  regardless of `max_turns`; the form previously accepted and saved the
  field on those providers with no indication it does nothing. The form now
  reads the existing `maxTurns` provider capability (already correct in
  `providerCapabilities.ts`) and shows an inline warning under the field
  when a cap is set (`max_turns > 0`) on a provider whose support is not
  `full`, mirroring the existing cost-tracking/subtasks/session-resume
  capability warnings (#286).
- **`qwen_code`'s `command_denylist` is now enforced; the no-op
  `command_allowlist` mapping was removed.** `buildQwenArgs` previously
  translated `command_allowlist` patterns into `--allowed-tools
  Bash(pattern)` entries, which restricted nothing — that flag only bypasses
  qwen's confirmation prompt, and the runner always passes `--approval-mode
  yolo` (auto-approve everything) on top, so an operator could set command
  restrictions on a `qwen_code` agent config and get zero enforcement with no
  error. `command_denylist` was not wired to the CLI at all, even though qwen
  v0.21.0 exposes `--exclude-tools`, which folds into its `permissionsDeny`
  policy and is honored even under `--approval-mode yolo`. The dead allowlist
  loop has been removed (the capability matrix already records
  `commandAllowlist` as unsupported for this provider) and `command_denylist`
  patterns are now appended as `Bash(pattern)` entries to `--exclude-tools`,
  mirroring the existing `--allowed-tools` convention. The exact glob
  granularity qwen's deny path accepts for `Bash(pattern)` has not been
  confirmed against a live run — see `docs/providers/qwen_code.md` for the
  caveat — so the capability matrix marks it `partial` rather than `full`
  pending live verification (see #285).
- **`opencode` runs that crashed with no output were silently reported as
  "completed" instead of "failed".** `OpencodeRunner.Run` fell through to
  the success path whenever the CLI exited non-zero without a recognized
  rate-limit/transient error *and* without a parsed `OUTCOME` marker, unlike
  every other CLI provider (`codex`/`gemini`/`qwen`), which already treat
  `err != nil && outcome == ""` as a failure. Found while generalizing the
  provider lifecycle test harness (#251) — a task whose `opencode` run
  crashed silently stuck as "completed" with an empty outcome, so it was
  never marked failed or retried.
- **`ws.ServeWS`'s origin-based CORS check silently rejected every
  legitimately configured `CORS_ORIGINS` value.** `CORS_ORIGINS` entries are
  full origins (scheme + host, e.g. `http://localhost:5173`), matching the
  exact-string comparison the `CORS` HTTP middleware does — but they were
  passed straight through as `nhooyr.io/websocket`'s `OriginPatterns`, which
  matches only the request Origin header's *host* (no scheme). A configured
  non-wildcard `CORS_ORIGINS` therefore never matched, and every WS upgrade
  from an allowed browser origin was rejected as "not authorized". Found by
  the new `ServeWS` origin tests (#251); `ServeWS` now strips the scheme
  before building the pattern list, matching the middleware's semantics.
- **Migration `008_agent_runs_fk_set_null`'s down migration was broken and
  would fail if ever actually rolled back.** Its down script recreated
  `agent_runs` with 8 columns, omitting `stored_info` (added by an earlier
  migration, 006), so rolling 008 back after 006 had already run failed with
  a SQLite column-count mismatch (`INSERT INTO agent_runs_old SELECT * FROM
  agent_runs` — 9 source columns into an 8-column table). All 49 down
  migrations existed but none had ever actually been executed end-to-end in
  CI; a new up→down→up round-trip test (`TestMigrationsUpDownUpRoundTrip`,
  #251) now runs every down migration in the chain against a scratch SQLite
  file on every PR, and caught this on its first run. Fixed by including
  `stored_info` in both the recreated table and the `INSERT`'s column list.
- **Session resume now works for `qwen_code`, `codex_cli`, and `opencode`, not
  just `claude`.** `Dispatcher.resolveAgentConfig` previously gated its prior-
  session lookup to `provider == "claude"`, so `ResumeSessionID` was always
  empty for every other provider and their already-correct `--resume`/`resume
  <id>`/`--session` invocations were dead code (see #281). The gate is now a
  `providerSupportsResume` check covering `claude`, `qwen_code`, `codex_cli`,
  and `opencode`. `opencode` additionally needed its own fix (#283):
  `classifyOpencodeJSON` was discarding the `sessionID` field the CLI stamps
  on every NDJSON event, so the runner never had an id to persist in the first
  place — it's now parsed and threaded onto both completed and failed
  `agent.Result`s. `gemini_cli` is intentionally **not** included yet: its
  runner also records a session id and its `--resume` invocation is correct,
  but `GeminiRunner` scopes `GEMINI_CLI_HOME` to a per-run temp directory that
  is deleted when the run ends, destroying the CLI's own session storage
  before a later run could resume it. That storage-lifetime problem is
  tracked separately in #284. See
  [docs/agents.md § Session Resume](docs/agents.md#session-resume) and the
  updated `qwen_code`/`codex_cli`/`opencode`/`gemini_cli` provider docs.
- **`opencode` runs now record token usage and cost, so cost budget caps
  fire and the Dashboard's cost aggregation is accurate for this provider**
  (#287). `classifyOpencodeJSON` previously only read `part.reason` off the
  `step_finish` NDJSON event, leaving `input_tokens`/`output_tokens`/
  `cost_usd` at `0` for every opencode run despite the CLI (`opencode-ai`
  v1.18.6) actually emitting both a `cost` number and a `tokens.
  {input,output,...}` object on that event. `cost` is authoritative (reported
  directly by the CLI, like `claude`/`qwen_code`), not estimated. Since
  `step_finish` fires once per step rather than once per run, and opencode's
  own SQLite `session` table stores a single cumulative cost/token row per
  session, the runner takes the *last* `step_finish`'s values rather than
  summing across steps. Usage/cost is now persisted on every run outcome,
  including failed and timed-out runs, since money may already have been
  spent. `costTracking` is now `full` in the provider capability matrix; no
  mid-run cost watchdog is wired up for this provider yet (usage is only
  known at end-of-run), so `costWatchdog` remains `none`. See
  [docs/providers/opencode.md § Cost & Usage Reporting](docs/providers/opencode.md#cost--usage-reporting).
- **The provider capability matrix now matches what the providers actually do.**
  An audit against the installed CLIs (`claude` v2.1.220, `@qwen-code/qwen-code`
  v0.21.0, `@google/gemini-cli` v0.52.0, `@openai/codex` v0.145.0, `opencode-ai`
  v1.18.6) and this codebase's own runners found eight rows that over- or
  under-stated support. `frontend/src/lib/providerCapabilities.ts` — read by the
  agent/provider config forms to warn about gaps at config time, and the source
  of `docs/agents.md`'s generated table — has been corrected, along with the
  affected per-provider docs:
  - `claude` **image attachments** were listed as supported "via `--image`". The
    `claude` CLI has no `--image` flag, so a task with attachments currently
    fails at launch — now marked unsupported and flagged as a known bug.
  - `gemini_cli` and `codex_cli` **`max_turns`** were listed as enforced. Neither
    CLI has a turn-cap flag and neither runner passes one; both are now ❌, and
    the note explains that only the run timeout bounds those runs.
  - `qwen_code` **cost tracking** claimed authoritative cost. Qwen's stream-json
    `result` carries token usage but no `total_cost_usd`, so it is tokens-only
    like `gemini_cli`/`codex_cli` — a cost budget cap will not reliably fire.
  - `qwen_code` **command allowlist** was listed as enforced. Qwen's
    `--allowed-tools` only bypasses confirmation, and the runner always passes
    `--approval-mode yolo`, so allowlist entries are a no-op.
  - `anthropic`/`llm` **task-editor tools** said "4 of 5"; the sidecar exposes
    six tools (seven with `create_subtask`) and these providers implement five
    natively, so it is now "5 of 7". The MCP-backed providers' "All 5" notes and
    the generated row label were stale for the same reason.
  - **Session resume** notes blamed missing CLI flags. The real reason
    `qwen_code`/`gemini_cli`/`codex_cli` never resume is that
    `Dispatcher.resolveAgentConfig` gates resume to the `claude` provider;
    `opencode` differs again in never recording a session id at all.
  - **`opencode` cost tracking** blamed the CLI. Opencode's `step_finish` part
    does carry `cost` and `tokens`; the gap is that `classifyOpencodeJSON`
    doesn't read them. Its docs also wrongly listed rate-limit detection as
    unimplemented (it is implemented) and image attachments as impossible
    (`opencode run` has `-f`/`--file`, just unwired).
- **The `claude` provider no longer passes an unsupported `--image` flag.**
  `buildClaudeArgs` used to append one `--image <abs-path>` flag per task
  attachment, but the `claude` CLI has no `--image` flag and rejected it at
  argument parsing — so every `claude`-provider run against a task with at
  least one attachment failed instantly, before any model call. The flags are
  no longer sent; attachments are still made available to the agent as files
  under `.task_attachments/` in the run's worktree (listed in the prompt), so
  no capability is lost. See `docs/providers/claude.md` § Image Attachments.
- **Task read paths no longer scale with total task count.** `GET /tasks` and
  `GET /tasks/{id}` computed their derived dependency-count and subtask-rollup
  badges by self-joining/scanning the *entire* `tasks` table on every
  request, regardless of page size — so a single-task fetch cost the same as
  listing every task in the system. Those queries are now scoped to just the
  ids being returned (`ListTaskDependencyCountsForTasks` /
  `ListSubtaskRollupsForParents`, new migration `049_task_read_indexes` adds
  the `tasks(created_at DESC, id DESC)` index backing cursor pagination).
  Separately, the SQLite connection pool was capped at a single connection,
  which serialized every read behind every write (and behind every other
  read) and defeated WAL mode entirely; the pool now allows several
  concurrent connections, with `_txlock=immediate` added to the DSN so
  write transactions take SQLite's write lock up front instead of risking a
  `SQLITE_BUSY` upgrade race between connections.
- **Two-column forms no longer collapse into overlapping, unreadable fields on
  mobile.** The Agent config, Provider config, Templates, and schedule forms use
  a `grid-cols-1 sm:grid-cols-2` layout, but their full-width rows hardcoded
  `col-span-2`. At mobile widths that spanned a second column the grid didn't
  have, so CSS created an implicit one: the single-width fields were crushed
  into a sliver of the first track while their labels overlapped each other and
  hint text wrapped one word per line. Those rows are now `sm:col-span-2`, so
  everything stacks in a single full-width column below the `sm` breakpoint.
- **The GitHub issue fetch no longer silently truncates at 200 issues.** It now
  paginates the full result set. Beyond dropping issues outright on busy repos,
  the cap would have made the new reconciliation mistake a truncated page for a
  closed issue.
- **A hung `git` call on one repo could halt agent dispatch system-wide.**
  Every `git` shell-out the agent package makes (worktree `fetch`/`add`,
  safety-net `commit`, `push`, subtask `merge`, branch cleanup) is now bounded
  by a configurable `GIT_TIMEOUT` (default `120s`); a stalled remote or a
  stuck interactive credential prompt previously hung that `git` call
  indefinitely, which blocked the dispatcher's serial sweep loop and stalled
  agent dispatch for every task on every repo until the process was
  restarted. A timed-out `git` call now fails just that one task within
  `GIT_TIMEOUT`, is classified as a transient failure (eligible for the
  existing retry policy) instead of a silent stall, and logs the repo,
  command, and elapsed time so the culprit is obvious. Interactive credential
  prompts are also suppressed (`GIT_TERMINAL_PROMPT=0` and related env vars)
  so an auth prompt fails fast rather than blocking on stdin. See
  [getting-started.md](docs/getting-started.md#backup). Dispatching tasks
  concurrently (rather than serially) was considered but deferred as a
  separate, larger change.
- **CLI agent output streams no longer silently truncate at 1 MB (or 64 KB on
  stderr) and wedge the run until its timeout.** Every CLI provider
  (claude/codex/opencode/qwen) scanned its subprocess's stdout with a capped
  `bufio.Scanner` and never checked `scanner.Err()`; a single line over the
  cap — routine when an assistant message quotes a large file a tool
  Read/Wrote — made the scanning goroutine exit silently, dropping the rest
  of that stream with no log entry and leaving nothing draining the pipe, so
  a still-writing child could block and the run wouldn't end until the outer
  timeout (default 600s) fired. Stderr had no explicit buffer at all, so it
  hit the default 64 KB cap even sooner. A shared `scanLines` helper now
  raises the buffer to 8 MB for both streams, keeps draining the pipe after
  an oversized line so the child can never block on it, and surfaces a
  visible warning in the run log plus a failed (not `completed`) result when
  the cap is hit.

### Changed
- **A run that exhausts `max_turns` now escalates to `waiting_human` instead
  of silently re-dispatching with a fresh turn budget.** Previously, hitting
  the configured turn cap ended the run as a plain `failed`/genuine failure
  (or, for `claude`/`qwen_code`, a `completed`+`failure` outcome) — either
  way the task's dispatch lock cleared and the very next sweep re-picked it
  up with a brand-new `--max-turns`/tool-use-loop budget, so `max_turns`
  bounded a single run but nothing at all bounded the task: it could loop
  indefinitely, burning tokens against the cost budget, with no human ever
  notified. Turn exhaustion now gets its own classification
  (`RunClassificationTotal{max_turns}`, distinct from `genuine`) and escalates
  the run straight to `waiting_human` with an explanatory note ("Agent hit
  its turn limit (N turns) without completing…"), a `task.needs_human` event,
  and the task's active-run lock left in place — the same shape as the
  existing auth-failure, exhausted-retry-budget, exhausted-cost-budget, and
  rework-loop escalations. It does not consume the transient-retry budget.
  Applies to every provider that actually enforces the cap: `claude`,
  `qwen_code`, `anthropic`, `llm` (`codex_cli`/`opencode` don't enforce
  `max_turns` at all — tracked separately). To continue past the cap, raise
  `max_turns` on the agent config or reply to / re-dispatch the run. See
  [docs/agents.md § Retry Policy](docs/agents.md#retry-policy).
- **The Logs tab now shows one row per tool call instead of three blocks.** A
  tool result is folded into the call that produced it, so the row carries the
  tool name, its command/arguments, and an outcome chip (`ok`, `40 lines`,
  `error`, `running`); the disclosure arrow reveals the full output. Previously
  the separate result row showed a reflowed 120-character preview of the output
  and expanding it repeated that same text in full — the preview is gone, so
  the output is shown exactly once, untruncated. Failures still auto-expand, a
  result short enough to fit on the row is shown inline with no arrow, a call
  still awaiting its result is marked `running` while the run is live, and a
  result whose call isn't loaded still renders on its own row.
- The Repos help modal described issue import as create-only, which is no
  longer accurate; it now covers ongoing sync, the update policy, what happens
  when an issue closes, and comment sync.
- **GitHub issue import now creates a repo's new tasks in a single DB
  transaction per sweep, and emits one batched `task.created_bulk` WebSocket
  event instead of one `task.created` per issue.** Previously each imported
  issue was its own implicit commit (repeatedly acquiring SQLite's
  single-writer lock) and its own broadcast event (each triggering a
  per-client task refetch) — a large backlog import could contend with other
  writers and flood connected clients. See
  [task-sources.md](docs/task-sources.md) and
  [websocket.md](docs/websocket.md).
- **Frontend hardening pass** (#253):
  - Routes are now code-split with `React.lazy`/`Suspense`, so the two
    heaviest per-route dependencies — `@xterm/xterm` (Chat) and
    `@xyflow/react` + `dagre` (Workflow) — no longer ship in the initial
    bundle for users who only ever open e.g. the Board.
  - The WebSocket client's reconnect now uses capped exponential backoff with
    full jitter instead of a flat 3s retry, so a server restart doesn't have
    every open tab hammering `/ws-ticket` + `/ws` in lockstep. A new
    connection-status indicator ("Live" / "Reconnecting…" / "Offline") is
    shown in the sidebar on every route.
  - The Board's `task.updated` WebSocket handler now applies the event's
    payload (already a full task) directly instead of triggering an extra
    `GET /tasks/{id}` round-trip.
  - Board task cards are keyboard-focusable and describe themselves via
    `aria-label`; Enter opens a focused task and a new `KeyboardSensor`
    (Space to pick up/drop, arrow keys to move) makes drag-and-drop between
    columns reachable without a mouse.
  - The page-level error boundary now resets when navigating to a different
    route, instead of a render crash on one page sticking across every
    subsequent navigation until a full reload.
  - `GitStateBadge`'s git/PR-state detail is now exposed via `aria-label`
    and `role="img"`, and the badge is a keyboard focus stop, instead of
    being reachable only through a native `title` tooltip.
  - Very large diff files in the PR review viewer now render collapsed by
    default instead of every line of every file being expanded up front.
  - Enabled `strictNullChecks` in the frontend TypeScript config (see
    `frontend/AGENTS.md`, previously inaccurately described as full `strict`
    mode) and fixed the errors it surfaced, including a task-card priority
    `<select>` that could cast an invalid value straight into `Task['priority']`.
- **Backend test coverage raised from ~59% to ~78%** with new behavioral
  tests for previously-untested handlers and helpers (task approve/reject,
  chat session CRUD, agent config get/delete, task run lookup, dashboard cost
  aggregation, `internal/storage.SeedDefaultWorkflow`, and several
  provider/workflow/template edit-conflict branches, among others). CI's
  coverage metric (`.github/workflows/ci.yml`, backend job) now excludes
  `internal/storage/gen/` (sqlc-generated, never hand-written) and
  `cmd/server` (pure process wiring — config/DB/HTTP setup and graceful
  shutdown, not meaningfully unit-testable) so generated/wiring code no
  longer drags the number away from reflecting real test coverage; the
  enforced floor moves from 55.0% to 76.0% against that filtered metric. See
  `backend/AGENTS.md` § Testing for how to reproduce CI's number locally.

### Dependencies
- **Dependabot now groups patch-only bumps into a single PR per ecosystem**
  (`.github/dependabot.yml`) for `gomod`, `npm`, `github-actions`, and
  `docker`, instead of opening one PR per patch release.
- Bumped `github.com/mattn/go-sqlite3` 1.14.48 → 1.14.49 and
  `github.com/prometheus/client_golang` 1.24.0 → 1.24.1 (backend); `oxlint`
  1.75.0 → 1.76.0, `@tailwindcss/vite` 4.3.2 → 4.3.3, `@playwright/test`
  1.61.1 → 1.62.0, `@vitejs/plugin-react` 6.0.3 → 6.0.4, and
  `react-router-dom` 7.18.1 → 7.18.2 (frontend). Routine dependency
  patch/minor bumps, consolidated from separate Dependabot PRs.

### Deprecated
- **The `anthropic` and `llm` providers are disabled for new/updated provider
  configs and may be removed in a future release.** Both run on a
  hand-maintained Go tool-use loop (rather than a vendor CLI) with permanent
  parity gaps — no `resolve_comment`/`create_subtask`, no MCP servers, no
  session resume, no image attachments, and cost estimated from a pricing
  table instead of reported. Neither is offered in the provider dropdown
  anymore, and `POST`/`PATCH` of a provider config using either now returns
  `400`. Existing provider/agent configs already on `anthropic` or `llm`
  keep dispatching and running exactly as before — nothing changes for them.
  The `openai` dropdown option (a dead alias for the same `llm` code path
  that always failed validation) is also removed. See
  [docs/providers/anthropic.md](docs/providers/anthropic.md) and
  [docs/providers/llm.md](docs/providers/llm.md).

## [0.14.0] - 2026-07-26

### Changed
- **Further mobile layout fixes across the app.** The Workflow page now stacks
  the flowchart preview and YAML editor vertically (instead of side by side)
  on narrow screens, and its legend wraps instead of overflowing. The Repo,
  Template, Agent, Provider, and Schedule forms now collapse their two-column
  field grids to a single column on narrow screens. The Model Pricing table
  narrows its price columns on mobile so the model name field stays usable.
  The Overview page's "Active agents" table now scrolls horizontally on
  mobile instead of clipping its content.
- **Repo Workflow field is now scoped to GitHub issue import.** Since a task's
  workflow is chosen per task, the repo's `workflow_id` is only still used by
  issue import (and scheduled tasks), so the Workflow picker on the Add/Edit
  Repo forms now appears only when "Import GitHub Issues" is enabled, and the
  repo cards no longer show a standalone workflow name. The repo list header
  also stacks vertically on narrow/mobile screens so badges no longer overlap
  the repo name.
- **Workflow is now chosen per task, not pinned to the repo.** The "New Task"
  modal gained a Workflow picker (sorted alphabetically, defaulting to the
  "Default" workflow) and no longer filters repos by the board's active
  workflow — every repo is selectable regardless of workflow. The REST
  `POST /tasks` and MCP `create_task` `workflow_id` parameter is optional
  again: when omitted, the task lands on the board's default workflow (the
  one named "Default", else the alphabetically-first workflow) instead of
  requiring the caller to know a workflow id or deriving one from the repo.
  A repo's own `workflow_id` setting is unchanged and still used by GitHub
  issue import and scheduled/recurring tasks, which have no interactive
  workflow picker.

- **Default task-start label now derives from the workflow instead of a
  hard-coded `not_ready`.** New tasks created without an explicit label —
  GitHub Issue imports, scheduled tasks, and API creates that omit a label —
  and schedules with an empty `target_label` now land on the workflow's
  *human-gate label*: the lowest `sort_order` `agent_ignore` label (falling
  back to the first label if none is marked `agent_ignore`). The
  "agent-in-progress" issue write-back likewise fires when a task first leaves
  that gate label rather than one named `not_ready`. For the default seeded
  workflow this is still `not_ready`, so nothing changes there — but custom
  workflows that don't define a `not_ready` label no longer strand imported,
  scheduled, or unlabeled tasks on a non-existent column. The schedule editor's
  default and "skips human review" warning now reference the selected repo's
  gate label.
- **Reorganized the sidebar navigation into collapsible categories.** The
  flat 11-link menu is now grouped into `Insights` (Cost & Usage,
  Performance), `Work` (Board, Chat), `Configuration` (Workflow, Agents,
  Providers, Repos, Templates), and `System` (Health), with Dashboard kept as
  a standalone top-level link. Each group header is a keyboard-accessible
  toggle (`aria-expanded`/`aria-controls`) that expands/collapses its links;
  the group containing the current route opens automatically and the
  expand/collapse state persists across sessions via `localStorage`. The
  link list scrolls independently of the pinned theme toggle so long,
  fully-expanded menus don't clip on short mobile viewports.
- **Dependency version bumps** (consolidated Dependabot PRs): frontend
  `@testing-library/jest-dom` 6.9.1→7.0.0, `tailwindcss` 4.3.2→4.3.3,
  `react-dom` 19.2.7→19.2.8, `@tanstack/react-virtual` 3.14.5→3.14.8,
  `oxlint` 1.74.0→1.75.0; backend `github.com/prometheus/client_golang`
  1.23.2→1.24.0; CI actions `setup-go` v6→v7 and
  `create-github-app-token` v2→v3.

### Added
- **In-app help modals on every page.** Each page (Overview, Board, Chat,
  Workflows, Agents, Providers, Pricing, Repos, Templates, Health, Cost &
  Usage, Agent Performance) now has an "i" info button that opens a modal
  explaining what the page does and how to configure it — aimed at making the
  app more approachable for new users. Closes on the ✕ button, clicking the
  backdrop, or pressing Escape.
- **New "Factory" dashboard visualization.** The Overview's optional "Visualize
  tasks" view gains a third style, chosen from a segmented Office / Robots /
  Factory picker (replacing the old standalone "Robots" toggle; an existing
  Robots preference migrates automatically). Factory renders each workflow label
  as a station on an assembly line — a machine that animates the work that label
  represents (intake hopper, CAD drafting gantry, inspection scanner, hydraulic
  press, QA bench, a laser-sweeping agent scanner, approval stamp, packing box) —
  while tasks ride conveyor belts between stations, gaining detail as they go.
  The belts are routed from the real flex layout, so they re-attach correctly
  (straight run, vertical drop, or wrap-around return) as the line reflows on
  mobile and desktop. The default workflow gets a bespoke machine per label;
  custom workflows collapse to the same three buckets the office scene uses.
- **Auto-open a GitHub PR when a task reaches a label.** Workflow labels gain a
  `create_pr` flag (settable in the workflow YAML). Any transition into a
  `create_pr` label — agent- or human-triggered — pushes the task's branch and
  opens (or reuses) a GitHub PR, the same result as clicking "Create PR"
  manually. Best-effort: a failure (no remote, `gh` unauthenticated, push
  rejected) is logged and the transition still commits. At most one label per
  workflow may set `create_pr` (saving a workflow with two is rejected).
- **Click-to-expand agent notes on the task overview.** The "Agent Notes"
  block on a task's Overview tab is now a clickable preview that opens a
  full modal (Escape or backdrop click to close) instead of a fixed
  `max-h-60` scroll box — makes long notes easier to read, including on
  mobile.
- **Opt-in browser notifications when a human is needed.** Frontend-only,
  driven by the existing app-wide WebSocket connection — no new backend
  infra, VAPID keys, or push subscription storage:
  - Fires when an agent calls the `request_human` MCP tool (or any other
    condition that already publishes `task.needs_human`, e.g. cost-budget
    or retry-budget exhaustion).
  - Fires when a task's label changes onto a "human gate" — a non-terminal
    label whose only outgoing transitions are human-triggered, or that has
    `agent_ignore` set — computed client-side from the active workflow.
  - Off by default; enable from the new toggle at the bottom of the nav
    sidebar, which requests browser notification permission from that click
    (a required user gesture). Degrades silently on browsers without the
    Notifications API.
- **User-editable model pricing table for cost estimation.** The USD-per-1M
  -token pricing used to estimate `anthropic`/`llm` run costs was previously
  a hardcoded Go map with no way to fix drift or add a missing model:
  - New `GET`/`PUT /api/v1/settings/pricing` endpoints backed by a new
    `model_pricing` table (migration 042), seeded from the previous hardcoded
    values so estimates are unchanged until a row is edited. `PUT` replaces
    the whole table in a transaction; add/remove/edit are all expressed
    client-side as a new full list.
  - A model not listed in `model_pricing` still falls back to the small
    hardcoded map in `internal/agent/providers/pricing.go`, so unlisted
    models keep working exactly as before.
  - A run whose model matches neither table now has `agent_runs.cost_unknown`
    set instead of silently showing `$0` — the run history UI renders "cost
    unknown" for it, distinct from a genuinely free run (e.g. `claude`/
    `qwen_code` under a Claude Max subscription, which never set this flag).
  - New **Configuration → Pricing** page: an editable table (model, input
    price, output price) with add/remove rows and a Save action.
- **DB-backed, UI-editable agent-log retention settings.** The agent-log
  cleanup pruner (`internal/logretention`) that deletes `agent_logs` rows
  for terminal-status runs older than N days is now configurable at
  runtime instead of env-var/restart-only:
  - New `GET`/`PUT /api/v1/log-retention/settings` endpoints (mirroring the
    existing `/api/v1/backup/settings` pattern) backed by a new singleton
    `log_retention_settings` table (migration 041).
  - The Health page has a new "Agent log cleanup" form to view/edit
    retention days and cleanup frequency without touching the server config.
  - `LOG_RETENTION_DAYS`/`LOG_RETENTION_INTERVAL` env vars now only seed the
    initial values on first migration; the pruner always runs and re-reads
    settings from the DB on each tick, so `days=0` (the default) fully
    disables cleanup and can be toggled at runtime without a restart. See
    [docs/backup.md#agent-log-retention](docs/backup.md#agent-log-retention).
- **Ingest GitHub PR review comments, changes-requested reviews, and failed
  GitHub Actions checks as task feedback.** `internal/ghsync`'s sweep now
  checks every task's open PR for new signals since the last sweep:
  - Inline review comments (with file/line anchors) are inserted as
    `task_review_comments` (tagged `source: "github"`), so they flow through
    the existing `OPEN REVIEW COMMENTS` prompt section and MCP
    `resolve_comment` loop right alongside locally-left comments.
  - `changes_requested` review bodies and failed check names/links (no
    anchor) are appended to the task's current run `Feedback`, rendered under
    the `FEEDBACK FROM PRIOR REVIEW:` prompt section the next time the task
    dispatches — closing the gap where a teammate's GitHub review previously
    had to be copy-pasted into a manual Reject note.
  - Ingestion is cursor-based and idempotent (`task_pr_review_state`,
    dedup by GitHub comment id / review timestamp / failing-check
    fingerprint) — re-sweeps never duplicate feedback. When the PR's head
    commit changes (the agent pushed), the review/check cursor resets so a
    fresh feedback cycle can start, while already-ingested inline comments
    are left as-is.
  - New per-repo opt-in `pr_review_auto_transition_enabled`: when set, a task
    with newly-ingested PR feedback is automatically moved along its
    workflow's "failure" human transition path (the same target as a manual
    Reject), so it lands back in front of an agent without a human clicking
    Reject. Off by default — feedback is still ingested and surfaced either
    way. Configurable from the Repos page.
  - Every step is best-effort (a `gh` hiccup on one signal never blocks the
    others or fails the sweep), mirroring the existing issue write-back
    error-handling style. See [task-sources.md](docs/task-sources.md).

### Fixed
- **Agent tool calls now show in the run log instead of vanishing.** An
  assistant stream-json message carrying a `tool_use` block (e.g. `Read`,
  `Bash`, `Edit`) was flattened to an empty log entry by the backend and never
  reached the UI's tool-call renderer — roughly a third of every run's log
  lines. The backend now preserves these as `tool_call` entries, and the
  frontend parser recognizes the real nested SDK shape
  (`assistant` → `message.content[].tool_use`) rather than only the
  hand-built top-level shapes. Plain user-turn text (not a `tool_result`) is
  now surfaced too instead of falling through to a raw JSON blob.

## [0.13.0] - 2026-07-21

### Added
- **Board MCP server (`mcp-board`) for creating tickets from a chat.** A new MCP
  server lets you work through a plan in a chat and have it create tickets on the
  board via `list_repos`, `list_workflows`, and `create_task`. It talks to the
  backend over REST and is deliberately separate from the per-run MCP sidecar, so
  the in-flow kanban agents never gain a task-creation tool. `create_task` drops
  tickets straight onto `work` by default.
  - **Wired into the in-app Chat tab:** set `MCP_BOARD_PATH` to the `mcp-board`
    binary (done automatically by the Docker images and `./dev.sh dev`) and the
    tools appear inside chat sessions for `claude`/`qwen_code` (via
    `--mcp-config`) and `gemini_cli`/`codex_cli` (via a per-session home dir).
  - **Or run standalone** and point an external chat client (e.g. Claude Desktop)
    at it: `go build -o mcp-board ./cmd/mcp-board`.

  See [board-mcp.md](docs/board-mcp.md).
- **`POST /tasks` accepts an initial `label`.** Task creation can now place a
  task directly on any column defined in its workflow (default remains
  `not_ready`). Since this is initial placement rather than a transition, it is
  not restricted to the workflow's transition edges; an unknown label returns
  `400`. See [api.md](docs/api.md).
- **Raw agent-log capture for parser debugging.** Set `AGENT_RAW_LOG_DIR` to
  dump every raw stream-json line from CLI providers (`claude`, `codex`,
  `gemini_cli`, `qwen_code`, `opencode`) verbatim to `<dir>/<run_id>.jsonl`
  before parsing, for reviewing what the CLIs emit vs. what we extract. Dev/debug
  only, off by default, no retention or compression. `dev.sh` gained a
  `--raw-log-dir <path>` flag to enable it. See
  [getting-started.md](docs/getting-started.md).
- **Workflow page: in-app help.** An info button in the Workflow editor header
  opens a modal explaining labels, transitions, trigger/path options, and the
  YAML configuration format, with a minimal working example — so the
  `docs/workflows.md` reference is discoverable right where you edit YAML.
- **Dashboard: "Visualize tasks" toggle.** A small, purely cosmetic switch in
  the Overview header swaps the "Task counts by label" chips for an animated
  top-down "office floor" — one station per workflow stage, each with its own
  workstation (desk, anvil, lab bench, charging rack, or podium). Little
  pixel-art workers (varied per person, not clones) queue up, walk to the
  workstation, and act out their stage there (drawing, hammering, a scanning
  robot, …) before stepping aside for the next; the crowd size scales with the
  task count. Everything sits on one continuous warm wood floor with the stage
  name + count on a sign hung over each workstation, so it reads as a single
  open workshop rather than a grid of boxes, with scattered plants, boxes, and
  mugs to feel lived-in. Stations reflow into a responsive grid, so it stacks
  cleanly on mobile instead of scrolling sideways. A "Robots" toggle swaps the
  whole crew for robot workers (boxy visored heads on the same animated bodies,
  so they still act out each stage), remembered separately in `localStorage`.
  Workers are directional — a true front, side profile, and
  back view, so someone walking up to a workstation shows their back and a
  side-walker shows a profile — with coherent top-left shading and a 3-frame
  walk cycle. Rendered on a Canvas 2D loop, honors
  `prefers-reduced-motion`, off by default, and the on/off state is remembered
  in `localStorage`.

### Changed
- **Frontend now consumes the generated OpenAPI types as its single source of
  truth.** `frontend/src/api/client.ts` previously hand-maintained a parallel
  set of ~30 request/response type definitions that could drift from the
  `openapi.yaml` spec. Those are now thin re-exports of the generated
  `src/api/types.ts` (produced by `npm run gen:api`), so the committed spec is
  the one place wire shapes are defined. Consumers import the same type names
  from `./client` unchanged. No runtime/API behavior change.
- **`openapi.yaml` gained required-field metadata and reconciled drift.** Every
  component schema now declares an accurate `required:` array (previously only
  `ProviderCheck` did), so generated fields that the server always populates are
  typed as present rather than optional. Also added fields that existed in the
  hand-written types but were missing from the spec — `Task.attachments`,
  `WorkflowTransition.path`, `AgentConfig.enabled`, and `AgentRun.stored_info` /
  `AgentRun.notes` — matching the backend's serialized JSON.
- **`AgentRun.agent_config_id` is now correctly typed as nullable.** The backend
  sets this column to NULL when the run's agent config is later deleted and emits
  `null` on the wire, but the spec typed it as a plain `string`. It is now
  `nullable: true`, so `AgentRun.agent_config_id` generates as `string | null`.

## [0.12.0] - 2026-07-16

### Added
- **Provider Config: provider/model/API keys split out into a separate,
  reusable entity.** Previously `AgentConfig` bundled which provider CLI to
  use, its model, and its env vars (API keys) together with unrelated
  workflow-behavior settings (system prompt, labels, retry policy, plugins,
  command filters, subtasks, cost caps), and chat sessions inlined their own
  provider/model. Provider/model/env now live on a new, standalone **Provider
  Config** (`GET/POST /api/v1/provider-configs`,
  `GET/PUT/DELETE /api/v1/provider-configs/{id}`; manage them from the new
  **Providers** page). `AgentConfig` and `ChatSession` each reference one by
  `provider_config_id`, so the same provider/API-key setup can be shared
  across multiple agent configs and chat sessions instead of being
  duplicated — and chat sessions can now pick up API keys from their provider
  config too. Existing agent configs and chat sessions are automatically
  migrated to their own provider config on upgrade (migration
  `039_provider_configs`), so no manual action is needed after updating.
  **Breaking change for direct API clients**: `POST`/`PUT /api/v1/agents` and
  `POST /api/v1/chat/sessions` now require `provider_config_id` instead of
  inlining `provider`/`model`/`env` — create or look up a provider config
  first. Deleting a provider config still referenced by an agent config or
  chat session is blocked with `409`. `GET /api/v1/health/providers`'s
  provider-specific checks (qwen/opencode binaries, anthropic/llm API keys,
  etc.) now key off providers actually referenced by an **enabled** agent
  config or a chat session, via their Provider Config — creating a Provider
  Config alone (with nothing pointing at it yet, or only a disabled agent
  config) no longer produces a readiness check for it.
- **Interactive terminal sessions.** A new chat surface runs a provider's CLI
  live in a real terminal against one of your repos — you see output as it
  happens, answer the CLI's own approval prompts, and type into it exactly like
  running the tool yourself — separate from the task board. Each session runs
  its interactive CLI (`claude`, `codex`, `gemini`, `qwen`, or `opencode`) in a
  PTY inside its own git worktree, streamed to an in-browser terminal over a
  WebSocket (`GET /api/v1/chat/sessions/{id}/terminal`). The process stays alive
  across disconnects, so a refresh reattaches to the same running session, and
  it launches with the CLI's resume flag so prior conversation history is
  restored from the CLI's own session store. The browser terminal automatically
  reconnects after a dropped connection (redrawing the current screen from
  server-side scrollback) and sends a periodic keepalive so idle sessions aren't
  reaped by proxy/load-balancer idle timeouts. New endpoints under
  `/api/v1/chat/sessions`.
- **Session resume for qwen/gemini/codex/opencode runs.** These providers now
  honor a stored provider session id (previously only the `claude` provider
  did), so resumed runs continue the same conversation.
- **Automatic-backup schedule settings, editable from the Health page.**
  Previously the automatic local-backup scheduler's interval and retention
  count were only configurable via `BACKUP_INTERVAL`/`BACKUP_KEEP` env vars
  at deploy time. They're now also DB-backed and editable at runtime via the
  new `GET`/`PUT /api/v1/backup/settings` endpoints and a form on the
  **Health** page — changes take effect on the scheduler's next run without
  a restart. Enforces a 10-minute minimum interval and a minimum retention
  count of 1; defaults to once a day, keeping the newest 7 snapshots (same
  as the previous env-var defaults). Whether the scheduler is enabled at all
  remains a deploy-time-only choice (`BACKUP_DIR`). See docs/backup.md.
- **Playwright E2E smoke tests** for the board and task-detail flows (load board,
  create a task, open task detail, verify the WS log pane mounts), running against
  the docker-compose stack in CI alongside the existing frontend test suite.

### Fixed
- **`GET /agents/models` no longer 404s for providers without a fixed model
  list.** Known providers that source their model from provider config (e.g.
  `qwen_code`) returned 404, logging a console error on the agent-config page.
  They now return an empty `models` list so the UI falls back to free-text
  model entry.
- **Chat terminal no longer sits flush against the pane edge.** The PTY
  terminal in the Chat page had minimal padding around it, so glyphs could
  butt right up against the window edge (and get visually clipped on
  rounded-corner window chrome). Padding increased from 8px to 16px.
- **Claude OAuth tokens are now auto-refreshed — no more 401s that require
  running Claude Code on the host.** The `claude` provider (and the dashboard
  usage widget) injected the raw `accessToken` from
  `~/.claude/.credentials.json` as `ANTHROPIC_AUTH_TOKEN`, which makes the CLI
  skip its own refresh flow; once the token expired (a few hours), every run
  401'd until Claude Code was run interactively on the machine to refresh the
  file. The server now checks the token's `expiresAt` before every use and, if
  it is expired or expires within 5 minutes, refreshes it via Anthropic's OAuth
  token endpoint with the stored refresh token, writing the rotated tokens back
  to the credentials file (atomic, 0600, other fields preserved) so the app and
  Claude Code stay in sync. If a token is expired and unrefreshable, nothing is
  injected so the CLI can attempt its own refresh flow instead of being handed
  a known-stale bearer token.
- **Transient-retry backoff windows are now honored.** A task backed off after a
  transient failure (`next_retry_at` set in the future) could be re-dispatched
  immediately instead of waiting. The dispatcher's pickup query compared the
  driver-stored timestamp (RFC3339 with a timezone offset) against SQLite's
  `CURRENT_TIMESTAMP` (space-separated UTC) as raw strings, so a future local
  time could sort below "now" and slip through the filter. Both sides are now
  normalized with `datetime()`.
- **New Task modal no longer shows a horizontal scrollbar.** The Type/Priority/Repo
  select row could grow wider than the modal when a repo name was long, since flex
  children default to a content-based minimum width; the columns and selects now
  shrink properly instead of forcing overflow.
- **Performance tab no longer loses agent-run history on upgrade.** The
  `039_provider_configs` migration rebuilds `agent_configs` (SQLite can't add
  a `NOT NULL REFERENCES` column in place). With foreign keys enforced,
  `DROP TABLE agent_configs` fired the pre-existing
  `agent_runs.agent_config_id ON DELETE SET NULL` action against every run
  row before the rebuilt table was renamed back into place, silently
  detaching all historical runs from their agent config and leaving the
  Performance tab empty (its per-agent-config stats query inner-joins on
  that column). Migrations now run without transaction-wrapping so a
  migration's own `PRAGMA foreign_keys=OFF/ON` (SQLite ignores this pragma
  inside a transaction) takes effect, and `039_provider_configs` disables FK
  enforcement around its table rebuilds. Deployments that already applied
  the old version of this migration will have their pre-upgrade run history
  permanently detached from its agent config; new runs are unaffected.

### Security
- **Worktree provisioning validates the task/session id.** The id becomes a
  filesystem path segment and git branch name; it is now rejected unless it is a
  single safe segment (no separators or `..`), closing a potential path-traversal
  vector. Ids are server-generated UUIDs, so this is defense-in-depth.

## [0.11.0] - 2026-07-13

### Fixed
- **Agent rework loops no longer run unbounded.** A reviewer/tester that keeps
  reporting the same finding used to send a task back along its `failure`
  transition (e.g. `agent-review → work`) indefinitely — every transition clears
  the dispatch lock, so nothing capped the cycle until a human paused the task.
  The pool now:
  - **Routes failure findings as feedback, not a plan.** When a run completes
    with `outcome='failure'`, its summary is stored on the run's `feedback` so
    the next agent receives it under "FEEDBACK FROM PRIOR REVIEW" (a fix
    request) instead of only under "NOTES FROM PRIOR AGENT" (which the default
    Worker prompt treats as an implementation plan — the reason the next Worker
    kept "verifying" already-committed code and advancing without fixing
    anything).
  - **Breaks the loop after a threshold.** If the same agent failure-path
    transition has already fired 3 times in a row — no human action and no
    success exit from the origin label in between — the task is escalated to
    `waiting_human` (run parked in `waiting_human`, task re-locked,
    `task.needs_human` published) rather than re-dispatched again.
- **Default Worker template prompt** now prioritizes any "FEEDBACK FROM PRIOR
  REVIEW" section and explicitly warns against signalling success just because
  code already exists from a prior run.
- **Closed a double-dispatch race on run completion.** The pool used to clear a
  task's active-run lock *before* performing the outcome transition; a dispatcher
  sweep landing in that window could start a second run for the same task. The
  lock is now cleared by the transition's atomic compare-and-swap (or explicitly
  only when no transition happens), eliminating the window.
- **Editing an enabled agent config no longer blocks on a shared-label
  "conflict".** `PUT /agents/{id}` used to reject enabling a config with a
  `409` if another enabled config already used the same label — a leftover
  guard from before priority-based failover, which relies on exactly that
  setup. Enabling now succeeds and surfaces the sharing config via the
  `X-Label-Conflict` header (matching `POST /agents` behavior), and the
  frontend shows it as an informational note instead of a blocking alert.

### Added
- **Recurring scheduled tasks from templates.** New `task_schedules` table
  (migration 035) links a task template to a repo and a cron expression. A
  background sweep (`internal/schedule`, same poll-loop shape as the GitHub
  Issues importer) fires due, enabled schedules and creates a task from the
  linked template, skipping the firing while an *open* task from a prior
  firing of the same schedule still exists (dedup by `source = "schedule"` /
  `source_ref`, "open" = not archived and not on a terminal workflow label) —
  so a weekly "upgrade deps" schedule never stacks on top of last week's
  unfinished run. Cron parsing/evaluation is a small dependency-free 5-field
  parser (`internal/cronexpr`) supporting `*`, comma lists, and `*/N` steps.
  New `/schedules` REST endpoints (list/create/get/update/delete), a new
  **Templates** page in the UI (`/templates`) with a per-template schedule
  editor offering hourly/daily/weekly-on-Monday cron presets plus raw cron
  entry. `target_label` defaults to `not_ready` (human review before an
  agent picks it up); setting it directly to a live agent label instead
  makes the schedule fully unattended — the UI flags this combination and
  recommends pairing it with a cost budget on the target agent config as a
  safety net. New `SCHEDULE_INTERVAL` env var (default `30s`) controls the
  sweep's poll interval. `POST`/`PUT /schedules` validate that `target_label`
  is actually one of the schedule's repo's workflow labels (`400` otherwise),
  and the Templates page's schedule editor picks the label from that
  workflow's label list instead of free text. See `docs/task-templates.md`.
- **Frontend component smoke tests (Vitest + Testing Library)** (#155).
  - New `jsdom`-backed component-test layer (`@testing-library/react` +
    `@testing-library/jest-dom` + `@testing-library/user-event`) alongside
    the existing pure-function tests in `src/lib`; wired into the same
    `npm run test:coverage` CI step, no new CI job/infra needed.
  - Coverage added for: `TaskBoard` drag-to-move (real `@dnd-kit` drag
    simulated via mouse events) including optimistic-update rollback on a
    rejected `moveLabel` call and the blocked-task move confirmation;
    `BoardPage` bulk actions (pause/archive/move-to, plus the
    partial-failure error banner); `TaskDetailPage` tab switching
    (Overview/Logs/Diff); `TaskActions` approve/reject/reply enablement
    rules.
  - New regression tests for review findings #138 (missing `Authorization`
    header on most API calls — since superseded on `main` by the runtime
    API-token flow, see `authToken.ts`) and #145 (attachment URLs ignoring
    `BASE_URL`, fixed alongside this change — see `[0.7.0]`'s Fixed section).
    #147 (hover-only board-card controls on touch) has a best-effort
    DOM-presence test only — jsdom can't simulate real hover/touch, so full
    verification is deferred to a future Playwright E2E layer (considered
    for this issue but descoped to keep this change to the component-test
    layer; see the issue for scope notes).
  - Minor testability-only production changes: `RunLogPane`/`DiffReviewPane`
    root elements got a `data-testid` hook; `client.ts` now exports its
    `BASE` constant so `TaskHeader` can share it instead of hardcoding an
    API path.

### Changed
- **Safer default `CORS_ORIGINS` and a startup warning for unauthenticated
  deployments.** The default `CORS_ORIGINS` is now
  `http://localhost:5173,http://localhost:8080` instead of `*`, closing a
  drive-by cross-origin attack where any web page open in the operator's
  browser could call the unauthenticated local API. Set `CORS_ORIGINS=*`
  explicitly to restore the old wide-open behavior. Starting with no
  `API_TOKEN` now logs a `slog.Warn`; the warning escalates when
  `CORS_ORIGINS=*` is also set.
- Documented 5 previously-undocumented WebSocket events (`task.updated`,
  `task.review_comments_changed`, `task.subtask_conflict`,
  `repo.clone_done`, `repo.clone_failed`), the dependencies/subtasks REST
  endpoints, the `cancelled` run status, and refreshed stale `CLAUDE.md`
  notes on `METRICS_TOKEN` and WebSocket ticket-based auth. Docs-only, no
  behavior change.

## [0.10.0] - 2026-07-12

### Added
- **Agent config priority / failover.** Agent configs now have a `priority`
  (lower runs first). When multiple enabled configs share a label, dispatch
  tries them in priority order and skips any that are currently
  rate-limited/out of usage credits, so a lower-priority config
  automatically takes over for a primary that's blocked, and fails back once
  the block expires.

## [0.9.0] - 2026-07-12

### Added
- **Container-local `qwen_code` config.** The backend now reads qwen settings
  from a repo-managed `backend/qwen-config/settings.json` (mounted read-write as
  the single `settings.json` under `QWEN_HOME=/home/node/qwen-home`) instead of
  the host's `~/.qwen`. This lets the container point the model `baseUrl` at
  `host.docker.internal:8081` (the host-published model server) while a host
  qwen install keeps `localhost` — the two no longer share, so neither breaks
  the other. `QWEN_RUNTIME_DIR=/tmp/qwen` and an entrypoint `chown` keep qwen's
  own startup writes (e.g. `output-language.md`) off the read-only-ish config
  and out of the repo. Provider API-key fields ship as the non-secret
  placeholder `local-no-auth` (the local server ignores the value but requires
  the env key to be present).
- **`dev.sh --all-cli`.** `dev.sh` (build-from-source) now supports the same
  `--all-cli` flag as `run.sh` (prebuilt-image runner): it sets
  `INSTALL_GEMINI_CLI`/`INSTALL_CODEX_CLI`/`INSTALL_QWEN_CLI=true` before
  invoking `docker compose up --build`, so a locally-built backend image also
  gets the Gemini, Codex, and Qwen CLIs installed alongside Claude.
  `docker-compose.yml`'s backend `build.args` now forwards these three
  build args (previously only `INSECURE_SKIP_SSL_VERIFY` was wired through);
  all default to `false` so a plain `dev.sh start` keeps the smaller
  Claude-only image. Only affects the Docker `start`/`restart` paths — `dev.sh
  dev` runs local processes with no Dockerfile involved.
- **Manually-triggered releases.** The "Release" GitHub Actions workflow now
  accepts a `workflow_dispatch` trigger with a patch/minor/major version-bump
  choice. Running it moves `CHANGELOG.md`'s `[Unreleased]` section under a new
  version heading, commits that to `main`, creates and pushes the matching
  tag, then runs the existing image build and GitHub Release steps — all in
  the same workflow run, since a tag pushed by the default `GITHUB_TOKEN`
  does not trigger a separate workflow run. The workflow fails fast if
  `[Unreleased]` has no content. Pushing a tag directly (`git tag vx.y.z &&
  git push origin vx.y.z`) still works unchanged for hotfixes.

### Fixed
- **`qwen_code` provider runs failed immediately with `Unknown arguments: max-turns, maxTurns`.**
  `buildQwenArgs` (`backend/internal/agent/qwen.go`) was passing `--max-turns`,
  but the `qwen` CLI's turn-budget flag is `--max-session-turns` — every run
  was rejected by the CLI's argument parser before any work happened. Fixed
  the flag name; docs and unit tests updated to match.
- **Manually-triggered releases built and published images twice.** The
  "Release" workflow's `prepare-release` job pushed the version tag using a
  GitHub App installation token (needed to push past `main`'s branch
  protection), but App-token pushes aren't subject to GitHub's same-workflow
  loop-prevention the way default-`GITHUB_TOKEN` pushes are — so that tag
  push retriggered the very same workflow via its `push: tags: v*` trigger,
  running the image build/publish/release jobs a second time for the same
  tag. Split into two workflows: `.github/workflows/prepare-release.yml`
  (`workflow_dispatch` only — bumps the changelog and pushes the tag) and
  the trimmed `.github/workflows/release.yml` (`push: tags: v*` only —
  builds/publishes images and creates the GitHub Release). The tag push
  from the first now triggers the second exactly once; the
  `git tag vx.y.z && git push origin vx.y.z` hotfix path is unaffected.

## [0.8.0] - 2026-07-10

### Added
- **`-all-cli` backend release image**. The release workflow now also builds
  and publishes `ghcr.io/myinisjap/agent-task-editor-backend:{version,latest}-all-cli`,
  a backend image with the Gemini, Codex, and Qwen CLIs preinstalled (the
  `INSTALL_GEMINI_CLI`/`INSTALL_CODEX_CLI`/`INSTALL_QWEN_CLI` build args, all
  enabled) alongside the default Claude-only image. `run.sh` gained an
  `--all-cli` flag to pull and run this variant instead of the default one
  (plain Compose users can set `ATE_CLI_SUFFIX=-all-cli`).
- **Claude CLI session/usage-limit 429s now retry at an exact reset time instead of generic backoff.**
  - The claude provider's stream-json `"result"` event parsing
    (`classifyStreamJSON` in `backend/internal/agent/claude.go`) now also
    surfaces the raw result text and the structured `api_error_status`
    field. `classifyResultMessage` treats `api_error_status == 429` as an
    unconditional rate limit, fixing a gap where Claude's session-limit
    message (e.g. `"You've hit your session limit · resets 6pm
    (America/Chicago)"`) carried no `"429"`/`"rate limit"` substring in its
    *text* and was previously mis-classified as a genuine failure (no
    retry) instead of a rate limit.
  - New `"session limit"`/`"usage limit"` patterns added to
    `errclass.go`'s classification table as a text-based fallback.
  - New `backend/internal/agent/claude_reset.go` (`parseClaudeResetTime`)
    parses the `"resets <time>(am|pm) (<IANA timezone>)"` clue out of the
    result text and resolves it to an absolute reset time, handling
    same-day/next-day rollover and a +1 minute retry buffer. Blank-imports
    `time/tzdata` so time zone parsing works in the production container
    (which has no `/usr/share/zoneinfo`).
  - `ClaudeRunner.runAttempt` now populates `ErrRateLimit.ResetAt` from this
    parsed time; the pool (`pool.go`) already scheduled retries against an
    exact `ResetAt` when present (falling back to exponential backoff
    otherwise) — no pool/dispatcher changes were needed.
- **GitHub Issues write-back (task-sources v2)** (#81). New opt-in per-repo
  `issue_writeback_enabled` flag (independent of `issue_sync_enabled`) writes
  an imported task's status back to the GitHub issue it came from: a comment
  linking the PR when the task first gets a `pr_url`, an `agent-in-progress`
  label applied the first time the task leaves `not_ready`, and the issue
  closed with a comment once the PR merges. All three are best-effort (a
  failed `gh` call is logged, never fails the caller/sweep) and idempotent
  via new per-task tracking columns (`writeback_in_progress_sent`,
  `writeback_pr_commented`, `writeback_closed`), not by scraping issue
  comments. Uses the same `gh` CLI auth as issue import and PR-state sync —
  no new credential surface. See [docs/task-sources.md](docs/task-sources.md).
- **Mobile polish: responsive Usage/AgentConfig pages, board swipe, and PWA install** (#148).
  - `UsagePage`'s cost-by-provider, cost-by-day, and cost-by-task tables are
    now wrapped in `overflow-x-auto` (previously `overflow-hidden`), so wide
    tables scroll horizontally instead of overflowing the viewport at narrow
    widths; `AgentPerformancePage`'s table already did this.
  - `AgentSidebar` now collapses into an off-canvas drawer under the `md`
    breakpoint (same fixed/backdrop/slide-in pattern as `NavSidebar`),
    with a new mobile-only "Configs" header bar in `AgentConfigPage`
    showing the selected agent's name and a button to open the drawer; the
    drawer closes itself after selecting an agent, starting a new one, or
    tapping the backdrop/✕.
  - `TaskBoard`'s mobile single-column pager (both the condensed and
    normal/expanded views) now supports left/right swipe to move between
    columns, via a small new `useSwipe` hook (`frontend/src/lib/useSwipe.ts`,
    native touch events, no new dependency) that ignores predominantly
    vertical drags so it doesn't fight the column's own vertical scrolling.
  - Added a web app manifest (`frontend/public/manifest.webmanifest`) plus
    `icon-192.png`/`icon-512.png`/`icon-512-maskable.png`, linked from
    `index.html` with relative paths so they resolve correctly both in dev
    (`/`) and behind the `/tasks/` production base path; `nginx.conf` gained
    `manifest-src 'self'` in its CSP and an explicit MIME-type mapping for
    `.webmanifest` (not in the base `nginx:alpine` image's `mime.types`).
    The app is now installable (Chrome "Install app" / Android "Add to Home
    Screen") and launches directly to the board.
- **`openapi.yaml` now documents all served `/api/v1` routes** (#140).
  - Added the 10 previously-undocumented paths: `PATCH /repos/{id}`,
    `POST /tasks/{id}/rerun`, `GET /tasks/{id}/github-status`,
    `PATCH /tasks/{id}/git-state`, `PATCH /tasks/{id}/pause`,
    `GET /uploads/{task_id}/{filename}`, `GET /github/auth-status`,
    `GET /workflows/{id}/export.yaml`, `PUT /workflows/{id}/yaml`, and
    `POST /workflows/import` — regenerated `frontend/src/api/types.ts` to
    match.
  - New `backend/internal/api/openapi_coverage_test.go` walks the router
    with `chi.Walk` and fails, listing every offender, if any served
    `/api/v1` route (or one of the small allow-listed root routes — `/ws`,
    `/metrics`, `/healthz`) is missing from `openapi.yaml`, closing the one
    direction the existing `gen:api`/sqlc codegen-drift checks didn't cover:
    the spec silently falling behind the router it's meant to describe.
- **Task cards and task detail now reachable on touch devices** (#147).
  - `TaskCard`'s select checkbox, pause, archive, edit, and delete controls
    were previously only revealed via `group-hover`, making them unreachable
    on touch devices (no hover) and effectively blocking the bulk "Move
    to…" toolbar action and per-card edit/pause from mobile. A new Tailwind
    `no-hover:` variant (`@media (hover: none)`) now forces these controls
    visible on devices without hover, leaving desktop's hover-reveal
    behavior unchanged.
  - The task detail Overview tab gained its own "Move to…" control next to
    the Label row, letting a task's label be changed from any device
    (mirrors the existing bulk "Move to…" toolbar action on the board).
- **Running version + update-available check on the Health page** (#151).
  - `cmd/server` now has a `Version` build var (default `"dev"`), stamped at
    build time via `-ldflags "-X main.Version=<tag>"`.
    `backend/Dockerfile` exposes this as an `ARG VERSION=dev`, and
    `.github/workflows/release.yml` passes `VERSION=${{ github.ref_name }}`
    as a Docker build-arg so release images are stamped with the git tag;
    local `docker compose build` leaves it at the `dev` default.
  - `GET /healthz` now returns `{"status":"ok","version":"<version>"}`
    (previously just `{"status":"ok"}`, sourced from Go's VCS build info
    rather than the release tag). `/healthz` was folded into
    `HealthHandler` (a new `Healthz` method) so it can read the
    injected version; it remains a fast, side-effect-free liveness probe.
  - `GET /api/v1/health/providers` (and the frontend's **Health** page) now
    includes a `version` check row showing the running build's version.
  - New opt-in `update_check` row (`UPDATE_CHECK_ENABLED` env var /
    `update_check_enabled` YAML key, default `false`) shells out to
    `gh release view` to compare the running version against the latest
    published GitHub release tag, warning when an update is available. It
    is disabled by default so the app never phones home without the
    operator explicitly opting in, and is best-effort: any failure (no
    network, `gh` not installed/authenticated, dev build) degrades to a
    `warn` status ("could not check for updates") rather than blocking or
    failing the endpoint — bounded by a 5s timeout so a hung `gh` call
    can't stall the Health page.
- **Unit tests for `internal/ghclient` and `internal/ghsync`** (#154).
  - `ghclient`: the `gh` CLI invocation is now routed through a small
    package-level `runGH` seam (defaulting to the real `exec.CommandContext`)
    so tests can feed canned `gh` output without shelling out to a real
    binary. New `ghclient_test.go` covers `GetPRForBranch`'s state
    normalization (`OPEN`/`MERGED`/`CLOSED` → `pr_open`/`pr_merged`/
    `pr_closed`), the "no PR yet" branch-exists-vs-not paths, `CreatePR`'s
    idempotent existing-PR short-circuit and the "already exists" race,
    `ListOpenIssues`'s label filtering, and `ParseGitHubName` (HTTPS/SSH,
    `.git` suffix, and junk-input rejection).
  - `ghsync`: `Syncer` now has an unexported `getPR` field (defaulting to
    `ghclient.GetPRForBranch` in `New`) so tests can drive `syncTask`/`sweep`
    against a fake PR lookup while exercising the real merged-PR cleanup
    path against a temp git repo. New `syncer_test.go` asserts that a
    `pr_merged` transition removes the task's worktree and force-deletes its
    local branch, that a `pr_closed`-without-merge transition leaves the
    worktree/branch untouched, that a no-op sync doesn't publish or write,
    that a previously-stored PR URL survives a state regression to a
    URL-less state, and that `sweep` skips repos with no GitHub remote
    without ever invoking the PR lookup.
  - No exported API changed — both seams (`runGH`, `Syncer.getPR`) are
    unexported implementation details with default values equal to prior
    behavior.
- **Agent log retention / pruning, and DB size on the Health page** (#150).
  - `LOG_RETENTION_DAYS` (env or `log_retention_days` in the YAML config)
    enables a built-in pruner that periodically deletes `agent_logs` rows
    belonging to runs in a terminal status (`completed`/`failed`/
    `waiting_human`) whose `completed_at` is older than that many days.
    Default is `0` (keep everything forever) — this is opt-in and does not
    change existing behavior unless explicitly configured, matching how
    `BACKUP_DIR` gates the backup scheduler. `LOG_RETENTION_INTERVAL`
    (default `1h`) controls how often the pruner runs.
  - The delete predicate requires both a terminal status *and* a non-null
    `completed_at`, so a still-`pending`/`running` run's logs — and the
    WebSocket replay path, which reads the live run's logs — are never
    touched.
  - New migration `032_log_retention` adds
    `idx_agent_logs_run_timestamp(agent_run_id, timestamp)` to keep the
    periodic prune scan cheap; new sqlc queries `DeleteOldAgentLogs` and
    `CountAgentLogsTotal`.
  - New `internal/logretention.Pruner`, modeled directly on
    `internal/backup.Scheduler`, wired into `cmd/server/main.go` alongside
    the backup scheduler.
  - Scope note: this release implements age-based retention
    (`LOG_RETENTION_DAYS`) only. A per-run row cap
    (`LOG_MAX_ROWS_PER_RUN`, capping retained rows per run to the newest N
    regardless of age) was considered but descoped as a possible
    fast-follow — it requires iterating every terminal run and adds
    complexity/perf risk that age-based pruning alone avoids.
  - `GET /api/v1/health/providers` (and the frontend's **Health** page) now
    includes a `db_size` check reporting the SQLite file size and total
    `agent_logs` row count, so bloat is observable before it slows down
    `VACUUM INTO` backups or log-list queries — independent of whether
    retention is enabled.
  - See the new "Agent log retention" section in [docs/backup.md](docs/backup.md)
    and the updated env var table in
    [docs/getting-started.md](docs/getting-started.md).
- **Named API tokens / actor identity in label history** (#45).
  - `API_TOKENS` env var (or `api_tokens` map in the YAML config) supports
    multiple named bearer tokens (format `name1:token1,name2:token2`).
    `BearerAuth` resolves a matching token to its name and stores it on the
    request context (`middleware.ActorFromContext`); the legacy `API_TOKEN`
    remains supported as an anonymous fallback (empty actor), so existing
    deployments are unaffected.
  - Human-triggered transitions (`PATCH /tasks/{id}/label`,
    `POST /tasks/{id}/approve`, `POST /tasks/{id}/reject`, and the `move`
    action of `POST /tasks/bulk`) now record the resolved actor name in
    `task_label_history.actor_id` instead of always leaving it blank.
  - New `GET /tasks/{id}/label-history` endpoint exposes the full
    label-transition audit trail for a task.
  - The task detail page's Overview tab now shows a "Label history" list
    (trigger, from/to label, actor, and note) below the run history.
  - Note: the `/ws` WebSocket endpoint still only supports the single
    legacy `API_TOKEN` for its `?token=` query param check — it does not
    resolve named actors (out of scope; it's not a human-triggered REST
    transition).
- **Grew the `anthropic`/`llm` providers' native tool-use loop toward parity
  with the MCP-backed CLI providers** (#83).
  - New editing tools: `str_replace(path, old, new)` (exact-match single
    replacement, so small edits don't require a full-file `write_file`
    rewrite within `max_tokens`), `list_dir(path?)` (recursive directory
    listing skipping `.git`/`node_modules`/dotdirs, capped at 2000
    entries), and `search(pattern, glob?)` (ripgrep-backed repo search,
    capped at 1&nbsp;MB of output). `search`/`list_dir`/`list_files` are
    read-only and are not gated by `command_allowlist`/`command_denylist`
    (same treatment as `read_file`).
  - New `get_task_transitions()` native tool, mirroring the MCP sidecar's
    tool of the same name — the transition list was already computed and
    passed to every run, it just wasn't exposed to these two providers'
    tool loop until now.
  - The backend Docker image now installs `ripgrep` (`rg`) by default,
    required by the new `search` tool.
  - Published a consolidated provider capability matrix in
    `docs/agents.md` (`claude`, `qwen_code`, `gemini_cli`, `codex_cli`,
    `anthropic`, `llm`, `opencode`), replacing scattered footnotes, and
    re-tiered `opencode` as chat-grade/experimental pending a spike into
    whether its project-scoped `opencode.json` config can inject the same
    MCP sidecar the other CLI providers use.
- **Task priority ordering for dispatch** (#44).
  - New `priority` column on tasks (`-1`=low, `0`=normal/default, `1`=high,
    `2`=urgent). The dispatcher's pickup query (`ListAgentPickupTasks`) now
    orders eligible tasks by `priority DESC, created_at ASC` instead of an
    unspecified order, so higher-priority tasks are dispatched first
    whenever there are more eligible tasks than free `MAX_WORKERS` slots.
    Priority affects ordering only — it never preempts an already-running
    task and doesn't bypass any other dispatch gate (paused, archived,
    blocked dependency, retry backoff, cost budget).
  - `POST /tasks` and `PATCH /tasks/{id}` accept an optional `priority`
    field (`-1`/`0`/`1`/`2`); invalid values are rejected with 400.
  - `GET /tasks` and `GET /tasks/{id}` now also surface a derived,
    read-time `queue_position` — a task's current 0-based rank in the
    priority-ordered pickup queue — null/absent when the task isn't
    currently pickup-eligible.
  - **UI**: a Priority selector on the new-task modal and the task card /
    task-detail edit forms, a small priority badge on cards with a
    non-default priority, and an "N in queue" hint on cards that are
    eligible for dispatch but waiting on a free worker.
  - See [docs/agents.md#task-priority](docs/agents.md#task-priority).
- **Prometheus `/metrics` endpoint** (#88).
  - `GET /metrics` exposes Prometheus text-exposition-format metrics: dispatcher/pool
    state (eligible tasks, dispatched runs, queue depth, busy/max workers,
    submit-rejections), run counters by terminal status and failure
    classification plus a duration histogram per provider, cost/token
    counters per provider/agent config, WebSocket hub stats (connected
    clients, broadcast drops), and GitHub sync-loop stats (ghsync/issue-import
    sweep durations, `gh` CLI call counts by command) — plus the standard Go
    runtime/process collectors.
  - Served at the server root (not under `/api/v1`) and **not** gated by
    `API_TOKEN`; independently gated by the new optional `METRICS_TOKEN` env
    var (unset by default, i.e. unauthenticated).
- **Ticket-based WebSocket auth** (#51) — moves the long-lived `API_TOKEN`
  out of the WebSocket URL, since query strings are commonly captured by
  reverse-proxy access logs and browser history.
  - New `POST /api/v1/ws-ticket` endpoint (normal Bearer auth) mints a
    random (`crypto/rand`), single-use ticket valid for ~30 seconds.
  - `GET /ws` now accepts `?ticket=<ticket>` and validates/consumes it —
    a replayed or expired ticket is rejected with `401`.
  - The frontend `WSClient` now fetches a ticket automatically before
    opening the socket whenever `VITE_API_TOKEN` is set; `connect()` is
    now `async`.
  - `?token=<API_TOKEN>` is kept as a **deprecated fallback** for existing
    setups/non-browser clients — each use is now logged as a warning
    server-side — and may be removed in a future release.

### Fixed
- **`/healthz` no longer requires `API_TOKEN`** (#139). It was accidentally
  mounted inside the BearerAuth middleware group in `router.go`,
  contradicting `docs/api.md`/`internal/api/CLAUDE.md` (which documented it
  as unauthenticated) and breaking the `docker-compose.yml`/
  `docker-compose.release.yml` healthchecks (plain `wget --spider`, no auth
  header) whenever `API_TOKEN` was set — the backend container would report
  unhealthy forever. Moved `/healthz` out of the BearerAuth group (alongside
  `/ws` and `/metrics`); added a router test locking in that it returns 200
  with no `Authorization` header even when a bearer token is configured.

### Changed
- **Dispatch queue visibility now gated on worker-pool saturation** (#152).
  - The `queue_position` field on task responses — and the "N in queue"
    badge it drives on `TaskCard` and the task detail header — is now only
    populated when the worker pool has no free slot (all `MAX_WORKERS` busy).
    Previously it was set for every pickup-eligible task regardless of
    whether a worker was actually free, so a task about to be dispatched on
    the very next sweep could misleadingly show as "waiting."
  - New `Pool.Saturated() bool` reports whether every worker slot is
    currently busy; the `RunCanceller` interface consumed by `TasksHandler`
    gained a matching `Saturated() bool` method (implemented by the agent
    pool, the interface's only real implementation).
  - No new WebSocket events or polling — the badge still rides the existing
    task fetch/refresh path (`GET /tasks`, `GET /tasks/{id}`) and clears
    automatically once a task starts running or a worker frees up.
- **Split the 1,400-line `handlers/tasks.go` into `tasks.go` /
  `task_response.go` / `task_uploads.go` / `task_bulk.go` / `task_runs.go` /
  `task_pr.go` by concern** (#156) — pure code-move refactor, no behavior,
  route, or handler-signature changes. `tasks.go` keeps CRUD, list/search,
  notes, and label transitions; `task_response.go` holds the wire-format
  wrapper and derived dependency/subtask/queue-position helpers;
  `task_uploads.go` holds the multipart attachment-save helper;
  `task_bulk.go` holds pause/archive toggles and the bulk action;
  `task_runs.go` holds the run list/get/logs/cancel/reply endpoints; and
  `task_pr.go` holds diff/PR/PR-URL/GitHub-status/git-state.

### Fixed
- **Frontend never sent the API token — enabling `API_TOKEN` broke the whole UI** (#138).
  - `client.ts`'s `request()`/`requestWithHeaders()` built request headers
    from only `Content-Type`; no `Authorization` header was ever attached,
    despite `frontend/src/api/CLAUDE.md` claiming otherwise. Setting
    `API_TOKEN` (item #1 on the security checklist) made every board/task/
    agent call from the stock UI fail with 401.
  - Even where a header *was* wired manually (`ws.ts`'s ticket mint,
    `WorkflowPage.tsx`'s YAML export, `HealthPage.tsx`'s backup download),
    it read the build-time `VITE_API_TOKEN` — a variable that can never be
    baked into the prebuilt GHCR image, so release users could not enable
    auth at all.
  - Replaced this with a runtime token: a new `src/api/authToken.ts` stores
    the token in `localStorage` (`ate_api_token`) and is the single source
    of truth for it. Every REST call (via new `authedRawFetch` in
    `client.ts`, used by `request()`/`requestWithHeaders()`/`agents.create`)
    and the WS ticket mint (`ws.ts`) now attach
    `Authorization: Bearer <token>` from this store.
  - On any 401, the stored token is cleared and a new
    `ApiTokenGate` component (`components/shared/ApiTokenGate.tsx`), mounted
    once around the whole app in `App.tsx`, shows a minimal "enter API
    token" screen; saving a token retries by reloading the page. With
    `API_TOKEN` unset on the backend, no request ever 401s, so the prompt
    never appears.
  - `VITE_API_TOKEN` still works as a dev-only convenience: if set, it seeds
    `localStorage` once (only when nothing is stored yet), so existing
    `.env.local` setups keep working without going through the prompt.
  - Docs updated: `frontend/src/api/CLAUDE.md`, `frontend/CLAUDE.md`, and
    `docs/getting-started.md`'s Authentication section now describe the
    runtime flow instead of the non-functional build-time one.
  - Also fixed a latent header-merge bug in `request()`/`requestWithHeaders()`
    (`...init` was spread after `headers:`, so a caller-supplied
    `init.headers` — e.g. `workflows.updateYaml`/`importYaml`'s
    `application/yaml` Content-Type override — silently dropped the whole
    merged headers object, including the Authorization header). Found and
    fixed alongside the frontend component-test layer (#155), which now
    pins both behaviors with regression tests.
- **Frontend: task attachment links ignored `BASE_URL`** (#145).
  `TaskHeader`'s attachment thumbnails/links hardcoded `/api/v1/uploads/...`
  instead of using the same `BASE_URL`-aware API root `src/api/client.ts`
  computes, so attachments 404'd whenever the app was served from a
  non-root base (e.g. the production `/tasks/` base set in
  `vite.config.ts`). `client.ts` now exports its `BASE` constant and
  `TaskHeader` uses it. Also found and fixed alongside #155's test layer.
- **`anthropic`/`llm` providers' `signal_complete` tool now actually
  transitions the task.** The tool schema advertised to the model took a
  `next_label` parameter (the exact label to move to), but the shared
  dispatch code always read an `outcome` argument instead — so a model
  faithfully following its own tool schema had its completion signal
  silently dropped (`Result.Outcome` stayed empty, which the pool's
  `resolveOutcome` cannot map to a transition), leaving the task stuck
  needing human intervention instead of advancing. `signal_complete` now
  takes `outcome: "success"|"failure"` for both providers, identical to
  the MCP sidecar's version, and the label is resolved automatically as
  intended. `docs/providers/anthropic.md` and `docs/providers/llm.md` are
  updated accordingly.
- **Dashboard "Needs your input" queue kept showing tasks that were already
  running again.** Replying to (or approving/rejecting) a `waiting_human`
  run dispatches a new run and repoints the task's active run at it, but
  deliberately leaves the old run's status as `waiting_human` as a
  historical record. `ListWaitingHumanRuns` had no way to tell a
  still-actionable `waiting_human` run apart from one that had already been
  superseded, so the old run kept showing up in the intervention queue
  forever, alongside the new run showing the same task as actively working.
  The query now joins on `tasks.active_agent_run_id` and only returns a
  `waiting_human` run while it's still the task's active run.

## [0.7.0] - 2026-07-09

### Added
- **SQLite backup/restore story** (#89).
  - `GET /api/v1/backup` streams a consistent point-in-time database
    snapshot as `application/octet-stream`, generated via SQLite's
    `VACUUM INTO` (not a raw file copy), so it's safe to call even while
    the app is under active write load. Bearer-gated like the rest of
    `/api/v1`.
  - **Health page**: a new "Download backup" button hits the endpoint
    directly from the browser (with the `Authorization` header set via
    `fetch`, since a plain link can't) for one-click on-demand snapshots.
  - Optional automatic local-backup scheduler: `BACKUP_DIR` (enables it),
    `BACKUP_INTERVAL` (default `24h`), and `BACKUP_KEEP` (default `7`,
    retention count) periodically write rotated `VACUUM INTO` snapshots to
    a local directory, pruning older ones beyond the retention count.
    Whether it's enabled is also surfaced as a new `auto_backup` check on
    `GET /api/v1/health/providers` (and therefore the Health page).
  - New `docs/backup.md` guide covering volume layout, the on-demand
    endpoint, a manual `sqlite3 VACUUM INTO` fallback, the automatic
    scheduler, a Litestream sidecar example for continuous offsite
    replication, and a restore procedure (stop backend, replace file,
    restart — migrations tolerate older snapshots by design).
- **Qwen CLI is now optionally installable in the backend Docker image.** The
  backend `Dockerfile` gains a new `INSTALL_QWEN_CLI` build arg (default
  `false`, mirroring `INSTALL_GEMINI_CLI`/`INSTALL_CODEX_CLI`) that, when set
  to `true`, `npm install -g @qwen-code/qwen-code`s the `qwen` binary the
  `qwen_code` provider expects. Previously the only way to get `qwen` on
  `PATH` inside the container was to install/mount it yourself. No backend
  Go code, health checks, or frontend changes were needed — `qwen_code` was
  already fully wired up; this only adds the missing in-image install path.

### Changed
- **Split the Dashboard into three focused pages** to reduce clutter on a
  single overloaded view. All three still read from the same `GET
  /dashboard` payload — this is a frontend-only reorganization, no backend
  or API changes.
  - `/` (**Overview**) now shows only the operational, "what needs my
    attention" sections: label count chips, active agents, and the human
    intervention queue (approve/reject).
  - `/dashboard/usage` (**Cost & Usage**, new page) holds the Claude
    5h/weekly rate-limit usage bars and the full cost/token breakdown
    (total, by provider, by day, by task).
  - `/dashboard/performance` (**Agent Performance**, new page) holds the
    per-agent-config performance table (success rate, avg/p90 duration,
    avg turns, retries, cost).
  - The sidebar nav gained two new top-level links, "Cost & Usage" and
    "Performance", alongside the existing "Dashboard" link.

### Security
- Pinned the CI and Docker builder Go toolchain to `1.26.5` (was the floating
  `1.26`) to pick up the fix for GO-2026-5856, a crypto/tls Encrypted Client
  Hello privacy leak that the previous run's resolved `1.26.4` toolchain was
  still vulnerable to, per `govulncheck`.

## [0.6.0] - 2026-07-07

### Added
- **Cost budgets per agent config / task, plus new cost analytics** (#42).
  - `max_cost_usd` (migration 030) can be set on an agent config and/or on
    an individual task as an advisory USD spending cap. Before each
    sweep-dispatch, the dispatcher sums a task's recorded `cost_usd` across
    every `agent_runs` row (any status — failed and in-flight runs count
    too) and compares it against the effective budget (the lower of the
    task's and its matched agent config's nonzero `max_cost_usd`; `0` means
    unlimited from that source). If the budget is already met or exceeded,
    the dispatcher skips starting a new run and instead creates a "phantom"
    `agent_runs` row directly in `waiting_human` status (no provider
    invocation), locks the task on it, and publishes `task.needs_human` with
    a `"budget exhausted: $<spent> of $<budget>"` message — mirroring
    `Pool.handleTransientFailure`'s escalation shape. This is **not** a
    mid-run kill switch: no supported provider can be aborted at a cost
    threshold, so a single expensive run can still land over budget; the
    guard only blocks the *next* dispatch. `DispatchReply` (human-initiated
    resume) is intentionally never budget-gated. See
    `docs/agents.md#cost-budgets`.
  - **Dashboard**: new "cost by day" table (last 30 days, newest first) and
    "top tasks by cost" table (top 20 by cumulative recorded cost, any run
    status), added to `GET /dashboard` as `cost_by_day`/`cost_by_task`.
  - **Board page**: a "Filtered cost" badge near the filter bar sums
    recorded cost across the currently-visible (filtered) tasks, backed by
    a new lightweight `GET /dashboard/cost-by-task` endpoint (full per-task
    rollup, no top-N cap).
  - **Task detail**: shows a task's cumulative cost (summed client-side
    over its already-fetched run list) next to its budget, if one is set,
    and a `max_cost_usd` field in the task edit form.
  - Agent config form gained a "Max cost per run (USD)" field alongside the
    existing retry-policy fields.
- **Two new agent providers: `gemini_cli` (Google's Gemini CLI) and
  `codex_cli` (OpenAI's Codex CLI)** (#84). Both follow the `qwen_code`
  precedent — a headless CLI subprocess with structured JSON output and MCP
  sidecar support (`signal_complete`/`request_human`/`update_task_notes`/
  `store_info`/`resolve_comment`) — but each has its own dedicated JSON event
  parser (`classifyGeminiJSON`/`classifyCodexJSON`) since neither CLI's
  output schema is compatible with claude/qwen's stream-json envelope.
  - `gemini_cli` runs `gemini -p ... --output-format stream-json --yolo`.
    MCP servers are wired via a fresh, per-run isolated `GEMINI_CLI_HOME`
    directory (a `settings.json` with `mcpServers`), since the Gemini CLI has
    no per-invocation `--mcp-config` flag. Token usage is reported by the
    CLI's terminal `result` event; no cost figure is reported, so `cost_usd`
    is left at `0`. Command allowlist/denylist are not enforced (no
    confirmed CLI flag).
  - `codex_cli` runs `codex exec --json --dangerously-bypass-approvals-and-sandbox`.
    MCP servers are wired via a fresh, per-run isolated `CODEX_HOME`
    directory (a `config.toml` with `[mcp_servers.*]` sections), since Codex
    only reads MCP config from a persistent config file. Token usage is
    reported by the CLI's `turn.completed` event; no cost figure is
    reported, so `cost_usd` is left at `0`. Command allowlist/denylist are
    not enforced — Codex has its own native sandbox/approval-mode system
    instead, which the `--dangerously-bypass-approvals-and-sandbox` flag
    (required for headless operation) bypasses entirely.
  - Both providers get a provider health-page row (binary-on-PATH +
    heuristic auth detection: `GEMINI_API_KEY`/`GOOGLE_API_KEY`/
    `~/.gemini/oauth_creds.json` for Gemini, `OPENAI_API_KEY`/
    `~/.codex/auth.json` for Codex).
  - The backend Docker image gains two new build args, both **default
    `false`** (unlike `claude`, which is installed unconditionally):
    `INSTALL_GEMINI_CLI` (`npm install -g @google/gemini-cli`) and
    `INSTALL_CODEX_CLI` (`npm install -g @openai/codex`).
  - New deep-dive docs: `docs/providers/gemini_cli.md`,
    `docs/providers/codex_cli.md`.

### Changed
- **Human-readable safety-net commit messages** (#63). The pool's automatic
  "safety-net" commit — created when an agent run completes with uncommitted
  changes left in its worktree — now leads with the task title as the commit
  subject (`<task title> (safety-net commit)`), with the task and run IDs
  demoted to `Task:`/`Agent-Run:` trailer lines, instead of a message
  consisting only of two bare UUIDs (`task <uuid>: agent run <uuid>`). These
  commits land in PR history and on `main` after merge, so this makes that
  history readable at a glance. (A configurable message template was
  considered but left out of scope; the format is currently hardcoded.)
- **Dependency maintenance** — consolidated the outstanding Dependabot updates
  into a single batch:
  - Frontend (npm): `@types/node` 24 → 26, `@xyflow/react` 12.11.1 → 12.11.2,
    `@tailwindcss/vite` 4.3.1 → 4.3.2, `oxlint` → 1.73.0. (The `typescript`
    5.8 → 6.0 bump is held back: `openapi-typescript@7.13.0` still requires a
    `typescript@^5.x` peer, which `npm ci` rejects.)
  - Backend (Go): `github.com/go-chi/chi/v5` 5.3.0 → 5.3.1.
  - Docker base images: `golang` 1.24 → 1.26 (backend builder) and `node`
    22 → 26 (backend runtime + frontend builder).
  - GitHub Actions: `actions/checkout` v4 → v7, `actions/setup-go` v5 → v6,
    `docker/metadata-action` v5 → v6, `softprops/action-gh-release` v2 → v3.
  - CI now builds/tests on Go 1.26 (`setup-go`) to match the Docker builder,
    and `govulncheck` is blocking again now that the toolchain is past 1.25.8
    (the previously-suppressed stdlib CVEs are fixed there). Docs and CLAUDE
    files updated to reflect the Go 1.26 / Node 26 container toolchain.

## [0.5.0] - 2026-07-07

### Added
- **Per-agent-config run analytics** on the Dashboard (#47). A new "Agent
  config performance" table breaks run history down by `agent_config_id`
  instead of just provider: success rate (completed/failed/waiting_human
  counts), average and p90 run duration, average "turns to done" per task,
  a transient-retry snapshot (tasks with retries, avg retries per task), and
  token/cost totals — so model/provider comparisons ("is opus-on-review
  worth it?") are data-driven instead of vibes. Backed by three new sqlc
  queries (`RunStatsByAgentConfig`, `ListRunDurationsByAgentConfig`,
  `ListTaskLastAgentConfig`) added to the existing `/dashboard` endpoint as
  `agent_config_stats`; no schema changes. See `docs/api.md` and
  `docs/agents.md` for the two attribution/semantics caveats (turns-to-done
  and retry counts are attributed to a task's *last* run's agent config, and
  the retry snapshot is live/resettable, not a lifetime count).
- The live agent-log view now renders background-task lifecycle events
  (`task_started` / `task_notification`) as readable system-event rows instead
  of dropping them: a start row shows the task type and a truncated description,
  and a notification row shows completion status (flagged as `Failed:` for any
  non-`completed` status) with a truncated summary. Handled across all three
  stream shapes the parser accepts (#96).
- **CI hardening** (#59). `ci.yml` now catches classes of drift and regression
  that previously only surfaced in production:
  - **`govulncheck ./...`** on the backend module on every PR (currently
    `continue-on-error: true` — it reports several reachable stdlib CVEs fixed
    only in Go 1.25.8+ while this repo is pinned to Go 1.24; flip it back to
    blocking once the toolchain is upgraded).
  - **Docker build check**: a new `docker-build` job runs `docker compose build`
    so a broken Dockerfile is caught at PR time — the backend image's final
    stage is also the agents' execution toolchain, not just a deployment
    artifact.
  - **Codegen drift checks**: `sqlc generate` and `npm run gen:api`
    (openapi-typescript) now run in CI and fail the build (`git diff
    --exit-code`) if `internal/storage/gen/` or `frontend/src/api/types.ts`
    don't match their sources, so generated code can no longer silently drift
    from `queries/*.sql`/migrations or `openapi.yaml`.
  - **Dependabot** (`.github/dependabot.yml`) for Go modules, npm, GitHub
    Actions, and the backend/frontend Dockerfiles, all on a weekly schedule.
  - **Coverage**: `go test -coverprofile` and `vitest run --coverage`
    (new `@vitest/coverage-v8` dependency + `test:coverage` script) now run on
    every PR with a step summary and an uploaded `*-coverage` artifact for both
    backend and frontend, so coverage trends are visible without a third-party
    account/token.

### Changed
- **Refactored `AgentConfigPage` and `TaskDetailPage`** (#62), the two largest
  and most feature-churned pages in the frontend, into smaller, independently
  readable units — no behavior change. `stores/agents.ts` now owns agent CRUD
  plus model/claude-options fetching (previously inline in the page);
  `AgentConfigPage` (836 → 233 lines) composes new `AgentConfigForm`,
  `ModelSelector`, `PluginMcpPicker`, `CommandFilterEditor`, `AgentSidebar`,
  and a shared `ChipPicker`. `TaskDetailPage` (1030 → 419 lines) composes new
  `RunLogPane`/`useRunLogs` (log fetch/pagination/virtualizer/WS replay),
  `DiffReviewPane`/`useDiffComments` (diff + inline review comments),
  `TaskHeader`, `TaskActions` (approve/reject/reply panel), and
  `RunHistoryList`.

## [0.4.0] - 2026-07-06

### Added
- Shift-click a task card's select checkbox to select every task between it and
  the last-clicked card in that column, instead of toggling one at a time.
- README and `docs/overview.md` now include real screenshots (board, task detail
  with live logs, diff viewer with an inline comment, workflow editor, dashboard,
  health page) and a hero GIF of the drag → dispatch → review → approve flow,
  plus a synced Features list between the two docs. `scripts/seed-demo.sh` seeds
  a throwaway demo repo and tasks for retaking these against a fresh
  `DB_PATH`-isolated instance.

## [0.3.0] - 2026-07-07

### Fixed
- **Agent config `resume_sessions`/`subtasks_enabled`/`enabled` round-tripped as raw
  0/1 instead of JSON booleans.** `GET`/`POST`/`PUT` on `/api/v1/agents` serialized these
  fields straight from their SQLite `INTEGER` storage, contradicting the OpenAPI schema's
  `boolean` type; a client that echoed a fetched config back into an update (unchanged
  fields included) would send `1`/`0` and get a generic `400 invalid request body`, since
  the server strictly required JSON `true`/`false` on write. Responses now always emit
  real booleans, and the write path additionally tolerates `0`/`1` for compatibility with
  any existing callers.
- **Frontend/backend healthchecks used `localhost`, which resolves to `::1` before
  `127.0.0.1` inside the containers**; since nginx (and the backend) only bind the IPv4
  wildcard address, the `::1` probe was refused and the containers reported `unhealthy`
  despite serving traffic fine. Healthchecks in `docker-compose.yml` and
  `docker-compose.release.yml` now target `127.0.0.1` directly.

## [0.2.0] - 2026-07-07

### Added
- **Dark / light theme toggle** (#87). The UI now ships an explicit theme switch in the
  sidebar; it defaults to the operating system's `prefers-color-scheme` and remembers your
  choice in `localStorage`. The theme is applied before first paint (an inline bootstrap in
  `index.html`) so there's no flash of the wrong theme on load. The dark theme is unchanged;
  the new light theme is derived by remapping Tailwind's color variables under a `.light`
  root class (see `frontend/scripts/gen-light-theme.mjs`), including light-appropriate diff
  and agent-log colors rather than naive inversions.
- **Agent-driven subtask decomposition** (#82, Mechanism 2). A planning agent can now
  split a large task into structured, dispatchable child tasks instead of leaving prose in
  `agent_notes`. Children are an implementation mechanism; the parent's branch (and single
  eventual PR) stays the only outward-facing artifact. Built on the Mechanism 1 dependency
  gate. Details:
  - **`create_subtask` MCP tool** (claude/qwen_code), exposed only when the run's agent
    config opts in (`subtasks_enabled`, off by default; `max_subtasks` caps children,
    default 10). Unlike the deferred result-file tools it writes **live** through the backend
    (`POST /tasks/{id}/subtasks`), so children appear on the board mid-run and the agent gets
    real task ids back. Guardrails: opt-in per config, per-parent cap, depth limit 1 (a
    subtask can't create subtasks), and a human gate — children land on the workflow's first
    `agent_ignore` label and a human releases them.
  - **Relationship model:** `tasks.parent_task_id` (grouping/rollup/provenance,
    `created_by_run_id`) plus an auto-created parent→child dependency edge (the dispatch
    gate). Deleting a parent orphans children to top-level rather than deleting them.
  - **Branch off parent, merge back:** a child's worktree is cut from the parent's branch. On
    reaching a terminal label a child's branch is **merged back into the parent's branch** (a
    plain merge commit, keeping per-child history) and its worktree/branch are removed —
    children never push to origin or open PRs. A conflicting merge-back is aborted cleanly,
    the child is flagged `merge_conflict`, and the parent's `work` agent is handed the
    conflict context to resolve it (`tasks.merge_status`: ``/`pending`/`merged`/
    `merge_conflict`).
  - **Parent auto-advance:** once every child is terminal and merged cleanly, the parent
    advances along its agent-success transition (`work → testing` in the seed workflow),
    recorded in history with a distinct `subtasks_complete` trigger. Degrades gracefully
    (leaves the parent for a human / the next dispatch) when it's paused, has a run in flight,
    or has no agent-success transition.
  - **Concurrency-safe:** merge-backs for one parent are serialized (children finishing at
    once can't corrupt the parent worktree), and a new per-repo git lock serializes the pool's
    commits/pushes against the coordinator's merges so concurrent tasks in a repo never race on
    the shared ref store. The whole loop is covered by a real-git end-to-end test driving the
    dispatcher, pool, engine, and coordinator with a fake provider.
  - **UI:** child cards carry a `↳ subtask` badge (click → parent) and a merge-status badge;
    parent cards show a `⑃ done/total` rollup with a conflict indicator; the task detail page
    gains a Subtasks section (parent link + merge state for a child; children list + bulk
    "release" for a parent); `GET /tasks?parent_id=` filters to one family. Agent config gains
    a "Subtasks" toggle, and the seeded Planner template enables it.

- **Task dependencies — "don't dispatch B until A is done"** (#82, Mechanism 1). Tasks can
  now declare peer dependencies: a task with any unsatisfied blocker is never picked up by
  the dispatcher, though a human can still move it anywhere on the board. A blocker is
  satisfied once it reaches a terminal label or is archived, so there are no invisible
  deadlocks. Blocked-ness is *derived at read time* — there's no status column to drift and
  no event needed when a blocker finishes; the next dispatch sweep simply sees the task
  differently. Details:
  - New `task_dependencies` table (migration `028`); both foreign keys cascade on delete, so
    deleting a task unblocks its dependents. Edges are within a single workflow in v1.
  - `ListAgentPickupTasks` grows a `NOT EXISTS (unsatisfied blocker)` clause — the whole gate
    is one SQL predicate alongside the existing pause/archive/retry filters.
  - Endpoints: `GET/POST /tasks/{id}/dependencies` and `DELETE /tasks/{id}/dependencies/{dep_id}`.
    Adding an edge rejects self-edges and cross-workflow edges (`400`), blockers whose workflow
    has no terminal label (`400`, an edge there could never satisfy), and cycles or duplicates
    (`409`, the error names the cycle path). Cycle validation runs transactionally.
  - Task list/detail responses carry derived `blocked_by_count` / `blocking_count` so the board
    renders "blocked by N" badges in one query (no N+1). Blocked cards are visually muted, and
    dragging a blocked card into an agent-triggerable column pops a confirmation.
  - Task detail gains a Dependencies section: a same-workflow blocker picker with live met/unmet
    state, plus the list of dependents. Edge changes and blocker completion refresh badges live
    via `task.updated` events.

  _Not included in this change:_ Mechanism 2 of #82 (agent-driven subtask decomposition — the
  `create_subtask` MCP tool, child branch/merge-back, conflict handling, and parent auto-advance)
  is a separate follow-up that builds on this dependency primitive.

### Fixed
- **Concurrent workflow transitions no longer race** (#49). `workflow.Engine.Transition`
  now performs the label update as a compare-and-swap (`… WHERE id = ? AND label = ?`,
  the expected from-label): if a concurrent transition already moved the task, the
  update matches 0 rows and the engine returns the new `ErrStale` sentinel instead of
  silently clobbering the other write and recording two history rows from the same
  source label. Handlers map `ErrStale` to HTTP `409 Conflict` so the UI can refresh
  and retry.
- **Orphaned `pending` agent runs no longer linger** (#50). The dispatcher now creates
  the run row and sets the task's active-run pointer in a single transaction (either
  both land or neither does), and the startup crash-recovery sweep marks runs stuck in
  `pending` — not just `running` — as `failed`, so a crash between the two writes can no
  longer leave a task permanently locked behind a run nothing points at.
- **ghsync no longer polls dead tasks forever** (#52). The GitHub PR-status sweep now
  selects only branch-bearing, non-archived tasks that aren't already in a terminal PR
  state (`pr_merged` **or** `pr_closed`) via a new `ListGhSyncEligibleTasks` SQL query,
  instead of listing every task and filtering in Go. Tasks that never get a PR, or whose
  PR was closed without merging, no longer cost a `gh` call on every sweep — keeping the
  external-call rate bounded by open work as the task table grows.
- **Repo auto-clone no longer times out or leaves partial directories** (#53). `git clone`
  for a `remote_url`-only repo now runs asynchronously (the repo row is created with
  `clone_status: cloning` and `POST /repos` returns immediately), so a slow clone of a
  large repo can't exceed the server's 60s `WriteTimeout` and get cut off mid-clone. On
  completion the row flips to `ready` (or `error` with `clone_error`, removing the partial
  clone directory) and a `repo.clone_done` / `repo.clone_failed` WebSocket event fires.
  The `Create` base-dir containment check now also resolves symlinks (via
  `filepath.EvalSymlinks`), matching `Update`, so a symlink under the base dir can no
  longer pass validation on create but fail on update.
- **`~/.claude.json` trust updates are now atomic** (#55). `setClaudeTrust` writes to a
  temp file in the same directory and `os.Rename`s it over the original (preserving mode
  `0600`) instead of rewriting in place, so a crash or a concurrent claude-CLI subprocess
  write can no longer corrupt the file and break every subsequent claude-provider run.

### Changed
- Centralized the previously-scattered provider error classification (login
  detection, transient-infra detection, and rate-limit detection) into a single
  pattern table in `backend/internal/agent/errclass.go` with per-pattern unit
  tests, so adapting to a CLI wording change is a one-line edit instead of a hunt
  across three files. Every failure now resolves to one explicit
  `classification` (`genuine`/`transient`/`rate_limit`/`auth`) that is logged on
  the failure log line, making misclassifications diagnosable from logs. The
  claude/qwen providers now prefer the typed stream-json `result` event over raw
  stdout/stderr sniffing when classifying a run.

### Security
- Fixed an authentication bypass where any request carrying an
  `Upgrade: websocket` header skipped bearer-token validation on **every** API
  route (not just `/ws`), letting a client read and write the full API without a
  token when `API_TOKEN` was set. The `/ws` route is now mounted outside the
  `BearerAuth` middleware (it does its own constant-time `?token=` check), and
  the header-based bypass has been removed.

### Added
- **Session continuity across runs** (#77). The `claude` provider now records each
  run's conversation `session_id` (from the CLI's stream-json envelope) and, when
  the same agent config runs the task again — a review rejection back to `work`, a
  re-run after a failure — resumes it with `claude --resume` instead of starting
  cold, so the agent keeps its full prior context instead of re-reading the repo
  to address a one-line note. Resumed runs send a condensed prompt (only the new
  feedback/reply/review comments); if the session no longer exists the runner
  falls back to a cold start automatically. Per-agent-config `resume_sessions`
  toggle (default on) for stages that should look at the work with fresh eyes.
  `qwen_code` records its session id but is not resumed yet.
- **Reply to a waiting agent** (#78). When a run pauses on `waiting_human`
  (`request_human`), a reply box on the task detail page — backed by
  `POST /api/v1/tasks/{id}/runs/{run_id}/reply` — answers the agent's question
  with text and starts a continuation run that resumes the same session (or
  starts cold with the reply injected as `RESPONSE FROM HUMAN` for non-resume
  providers). The task stays on its label — a reply is a conversation, not a
  workflow transition — the replied-to run keeps `waiting_human` (approve/reject
  parity), and the reply is recorded at the top of the new run's log.
- Cancel a running agent run: `POST /api/v1/tasks/{id}/runs/{run_id}/cancel` plus
  a **Stop run** button on the task detail page. The pool keeps a per-run cancel
  registry; cancelling cancels the run's context (killing CLI subprocesses via
  `exec.CommandContext` and aborting HTTP providers), then marks the run
  `cancelled` (not `failed`, and without consuming transient-retry budget),
  pauses the task so it isn't immediately re-dispatched, clears the active-run
  lock, and broadcasts `task.agent_done`. Fills the kill-switch gap where
  pausing only blocked *future* dispatch — a runaway agent no longer burns
  tokens until it times out.
- Provider health / onboarding status page (`Health` in the sidebar) backed by a
  new `GET /api/v1/health/providers` endpoint. Checks the claude CLI (present +
  authenticated), API keys for the anthropic/llm providers, qwen/opencode
  binaries (only for providers an enabled agent config uses), the MCP sidecar
  binary (`MCP_SERVER_PATH`), gh auth, and `REPO_BASE_DIR` — each rendered as a
  green/yellow/red row with a one-line fix hint. Turns the most common "why did
  my first run fail" support loop into a glance.

## [0.1.0] - 2026-07-04

First tagged release.

### Added
- Versioned multi-arch (amd64 + arm64) Docker images published to GHCR on every
  `v*` tag push, plus an automated GitHub Release.
- `docker-compose.release.yml` for running from the prebuilt
  `ghcr.io/myinisjap/agent-task-editor-{backend,frontend}` images instead of
  building from source.
- `run.sh` helper that injects the runtime env vars (repo mount, GitHub token,
  Claude auth, SSL bypass) and starts the stack from published images.
- Runtime `PUID`/`PGID` remap in the backend container: it steps down to the
  host user at startup (via an entrypoint + `su-exec`) so files agents write to
  bind-mounted repos are owned by you. Works for prebuilt images with no rebuild.
- Frontend unit tests (vitest) for the `src/lib` parsers — `parseAgentLog`,
  `parseWorkflowYaml`/`validateWorkflow`, `parseDiff`, `condensedBoard`, and
  `diffComments` — with real captured fixtures, wired into the frontend CI job.
- This changelog.

### Changed
- Backend image runs as the host user at runtime via `PUID`/`PGID` instead of a
  build-time `HOST_UID`/`HOST_GID` remap. `dev.sh`/`run.sh` set these from
  `id -u`/`id -g`; the build no longer bakes in a UID.
