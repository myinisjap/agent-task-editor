# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

To cut a release, run the "Release" workflow manually from the Actions tab and
pick a version bump (patch/minor/major) — it moves this file's `[Unreleased]`
section under a new version heading, commits that to main, tags it, then
builds and pushes the images and creates a GitHub Release using that section
as the release notes. The `[Unreleased]` section must have content or the
workflow fails.

Alternatively, for hotfixes where you want to hand-edit this file yourself,
add a `## [x.y.z] - YYYY-MM-DD` section below with the changes and push the
matching tag directly (`git tag vx.y.z && git push origin vx.y.z`), which
triggers the same build/release steps.

## [Unreleased]

### Added
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
