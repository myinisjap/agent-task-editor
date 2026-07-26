# Agent Task Editor — Improvement Audit

_Audit date: 2026-07-26 · against `main` @ v0.14.0 (6d82b09)_

Scope: full-stack review of the Go backend (180 files), React frontend (120 files),
provider subsystem, storage/migrations, CI/CD, ops posture, test suite, and product
feature surface.

---

## 0. Baseline health (verified, not assumed)

| Check | Result |
|---|---|
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `go test ./...` (22 pkgs) | **all pass**, no hangs; slowest `internal/agent` 15.2s |
| Backend coverage | **60.3%** |
| CI | golangci-lint, `go test -race`, govulncheck (blocking), sqlc drift check, `tsc -b`, oxlint, vitest, openapi-types drift check, compose build, Playwright E2E |
| Crash recovery | **Present and correct** — `cmd/server/main.go:106-120` fails orphaned `running`/`pending` runs and clears every `active_agent_run_id` before workers start |

This is a well-built, unusually disciplined codebase. The findings below are real, but
they sit on top of a solid foundation — the CI pipeline is better than most commercial
projects, and the docs are candid about their own limitations.

---

## 1. Critical

### C1 — Provider API keys are returned in plaintext by the API
`backend/internal/api/handlers/providers.go:24-42` — `List` and `Get` serialize the raw
`gen.ProviderConfig`, including the `env` column that stores `ANTHROPIC_API_KEY` /
`LLM_API_KEY`. The same blob is re-embedded into every agent-config response via
`agentConfigView.ProviderConfig` (`handlers/agents.go:71-95`). Grep for
`redact|mask|WriteOnly` across `handlers/` returns **nothing**.

Because `API_TOKEN`/`API_TOKENS` are empty by default (`config.go` `Defaults()`), a
default install serves every configured provider key to anyone who can reach the port.
Even with auth on, tokens have no scoping — any token holder reads every secret.

**Fix:** write-only `env` field — accept on write, omit or mask (`sk-…abcd`) on read,
in both the provider-config and agent-config response DTOs.

### C2 — Agent output streams silently truncate and can wedge the run
Every CLI provider sets a hard 1 MB scanner cap and **none** check `scanner.Err()`:

```
claude.go:241  opencode.go:82  qwen.go:161  gemini.go:220  codex.go:255
```
(verified: 5 `scanner.Buffer` calls, **0** `scanner.Err()` calls in the whole package)

One stream-json line over 1 MB — routine when an agent `Read`s or `Write`s a large file —
makes `Scan()` return false with `bufio.ErrTooLong`. The goroutine exits silently, the
rest of the stream is dropped with no log line, and because nothing drains stdout the
child can block on a full pipe until the 600s timeout fires. A data-loss bug that
presents as a mysterious timeout. Stderr scanners don't set a buffer at all, so they cap
at 64 KB.

**Fix:** one shared scan helper that checks `Err()`, emits a `LogSystem` warning, and
marks the run failed rather than silently completing.

### C3 — `ClearActiveAgentRun` is not scoped to the run that owns the lock
`backend/internal/storage/queries/tasks.sql:87-90` (verified):
```sql
UPDATE tasks SET active_agent_run_id = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
```
No `AND active_agent_run_id = ?` guard. `workflow/engine.go:174-176` also nulls it
unconditionally on any transition, and `MoveLabel` (`handlers/tasks.go:471-486`) does not
check whether a run is live.

Sequence: human moves a label mid-run → lock cleared → sweep dispatches R2 → R1 finishes,
its stale transition is rejected, and its cleanup path (`pool.go:556-561`, `:578`) clears
**R2's** lock → R3 dispatches alongside R2. Since `ensureWorktree`
(`dispatcher.go:400-424`) reuses `t.WorktreePath`, two agent processes then run in the
*same* git working tree — index corruption and lost work.

**Fix:** scope the clear to the run ID; make `MoveLabel` refuse or force-cancel when a
run is live.

### C4 — Cost budgets are inert for 3 of 7 providers
`codex`, `gemini`, and `opencode` never call `estimateCostUSD` (`parse_codex.go:32`,
`parse_gemini.go:81`, `parse_opencode.go:16`), so `CostUSD` stays `0.0` — and unlike the
anthropic/llm path there's no `CostUnknown` flag. `checkCostBudget`
(`provider.go:196-203`) gates the next dispatch on accumulated recorded cost, so
**`MaxCostUSD` never trips** for tasks on those providers, no matter the real spend.
A safety feature that is silently doing nothing. Codex and gemini already capture token
counts, so this is mostly a wiring fix.

### C5 — One hung `git` call halts dispatch system-wide
`dispatcher.sweep` (`dispatcher.go:96-98`) dispatches serially in-loop; `provisionWorktree`
runs `git fetch --prune` (`worktree.go:107`) and `git worktree add` (`worktree.go:123-129`)
with the **top-level app context** — there is no `context.WithTimeout` anywhere in the
non-test `agent` package. A flaky remote or credential-helper stall on one repo blocks the
sweep loop, which blocks `Run()`, halting dispatch for every task on every repo until restart.

---

## 2. High

| # | Finding | Location |
|---|---|---|
| H1 | `GET /api/v1/backup` streams the raw unredacted SQLite file — one request exfiltrates the whole secret store | `handlers/backup.go:32-73` |
| H2 | Full `os.Environ()` is forwarded to agent subprocesses that all have `Bash`; agent can `env` and read backend secrets. `dangerousEnvKeys` only blocks hijack keys (`PATH`, `LD_PRELOAD`), not confidentiality | `claude.go:199`, `cli.go:12-16` |
| H3 | Prompt injection: `buildPrompt` concatenates task title/description with **no delimiters**, and `extractOutcome` scans model text for a literal `OUTCOME: success` — a crafted GitHub issue body can forge a successful outcome | `prompt.go:22-24`, `parse.go:27-44` |
| H4 | `SetMaxOpenConns(1)` forces reads *and* writes through one connection, negating the WAL mode enabled right next to it; any slow query stalls the whole app | `storage/db.go:36` |
| H5 | `GET /tasks` **and** `GET /tasks/{id}` run `ListSubtaskRollups` — an unfiltered `tasks`-self-join — plus `ListTaskDependencyCounts` over the entire table, on every call. O(all tasks), so pagination doesn't help | `task_response.go:73-117`, `queries/tasks.sql:229-239` |
| H6 | The board's "agent running" indicator is **dead code**: `runningTaskIds` is created with `useState` and never populated (no setter destructured), then threaded read-only through 4 components | `BoardPage.tsx:22` → `TaskColumn.tsx:54`, `AgentGroupColumn.tsx:60`, `TaskCard.tsx:297` |
| H7 | `backend` service has **no `restart:` policy** in either compose file — only `frontend` has one. A backend panic stays down until a human notices | `docker-compose.yml`, `docker-compose.release.yml` |
| H8 | Docker healthcheck polls `/healthz`, which returns `{"status":"ok"}` unconditionally — no DB ping, no dispatcher liveness. A wedged backend reports healthy | `handlers/health.go:16-26` |
| H9 | Task text is passed via **argv** (`-p <prompt>`) for every CLI provider — visible in `ps`/`/proc/<pid>/cmdline`. Credentials are correctly kept in env, so this is inconsistent with the care taken elsewhere | `claude.go:82-83`, `qwen.go:55`, `gemini.go:113`, `codex.go:163` |

---

## 3. Medium

**Backend / data**
- No index supports `ORDER BY created_at DESC` on tasks — only `label` and `repo_id` are indexed (`001_init.up.sql:99-104`). Dispatcher hot path (`ListAgentPickupTasks`) runs correlated `NOT EXISTS` subqueries with no supporting index.
- Archiving never tears down the worktree — archived non-terminal tasks leak `.ate-worktrees/<id>` forever, and are excluded from the ghsync sweep.
- `TerminalManager` has no session cap or idle reap (`terminal.go:135-213`); each session holds a PTY subprocess + 256 KB scrollback indefinitely.
- No pagination on `GET /provider-configs`, `/agents`, `/repos`, `/workflows`, `/tasks/{id}/runs`.
- Inconsistent error shapes — `middleware/auth.go:68` and the chat/WS paths use plain-text `http.Error` instead of the `{"error":…}` convention in `respond.go`.
- Cache tokens (`cache_creation_input_tokens`/`cache_read_input_tokens`) are never parsed (`parse_streamjson.go:152-179`, `anthropic.go:192-195`) — systematic cost undercount on the direct-API provider when prompt caching is on.
- `REPO_BASE_DIR` unset by default lets any host path be registered, and agents run shell commands inside it.

**Frontend**
- **No route-based code splitting** — `App.tsx:6-18` statically imports every page, so `@xyflow/react` + `dagre` (WorkflowPage) and `@xterm/xterm` (ChatPage) ship in the initial bundle. No `lazy()` anywhere.
- WS reconnect is a flat `setTimeout(…, 3000)` with no backoff or jitter (`ws.ts:83-85`) — thundering herd on server restart.
- `FileDiffViewer.tsx:345-411` renders every line of every file, expanded by default — while `RunLogPane` already virtualizes with `@tanstack/react-virtual` for the same problem.
- Board drag-and-drop is **mouse/touch only** — `TaskBoard.tsx:94-97` registers no `KeyboardSensor`, and `TaskCard.tsx:186-190` isn't even a focus stop. A keyboard-only user cannot open or move a task.
- `ErrorBoundary` wraps all of `<Routes>` and never resets on navigation (`App.tsx:35`) — one page crash traps the whole app.
- `TaskDetailPage.tsx:75-84` initial load has no `cancelled` guard (unlike `useRunLogs.ts:45-53`) — stale response can clobber newer data.
- Every WS event triggers a **full REST refetch** (`BoardPage.tsx:92-98`) even though `task.updated` already carries the complete `Task`.
- No global WS connection-status indicator anywhere — a dropped socket shows silently stale data.
- **`strict` is not set in any `frontend/tsconfig*.json`** (verified), yet `frontend/src/api/AGENTS.md` claims "TypeScript strict mode enabled". ~63 `any` tokens in non-test source. `tsc -b` runs in CI but catches only syntax-level errors.

---

## 4. Test improvements

Coverage is real but unevenly distributed: **backend 60.3%**, `src/lib` 63% of files,
`components`+`pages` **26%**, stores **1 of 7**.

### 4a. Tests that would have caught the bugs above (none exist today)

| Bug | Test status |
|---|---|
| C1 secret exposure | **A test pins the vulnerability**: `providers_test.go:78` asserts `env` round-trips verbatim. A redaction fix requires *rewriting* this test, not just adding one. |
| C2 oversized line | None. `claude_test.go:15-50`'s `CLAUDE_TEST_HELPER` re-exec harness is the right vehicle — add a `>1MB` line mode. No other provider has a `Run()`-level harness at all. |
| C3 lock race | `engine_test.go:116` races the CAS well, but **nothing races a human `MoveLabel` against a still-`running` run** — the exact precondition. |
| C4 $0 cost budget bypass | None. Needs a dispatcher test asserting the bypass *before* the fix lands. |
| C5 hung git | None, and untestable today — `git()` shells out directly with no injectable seam. |
| H2 env forwarding | `cli.go` has **no test file**; `mergeEnv`'s blocklist is a completely unverified security control. |
| H3 prompt injection | `prompt_test.go` only checks section ordering; `parse.go`/`extractOutcome` has no test file. |
| H6 dead indicator | `TaskBoard.test.tsx:133,151,187` always passes `runningTaskIds={new Set()}`; `TaskCard.test.tsx` never passes `isRunning`. |

### 4b. The structural gap: WS wiring is never exercised
`BoardPage.test.tsx:39-45` and `TaskDetailPage.test.tsx:45` mock `wsClient.on` as
`vi.fn(() => () => {})` — a permanent no-op. **Every "WS event → derived UI state" path
in the app is therefore unverified**, which is exactly how H6 survived. The correct
pattern already exists in one place: `lib/useHumanNeededNotifications.test.ts:9-16`
captures the handler and invokes it manually. Generalize it.

`api/ws.ts` (117 lines, the core realtime engine) has **no test file** — reconnect,
ticket-refresh-on-401, resubscribe-on-reopen, and dedup are all unverified.

### 4c. Untested code carrying real risk
- **Providers:** `Run`/`binary`/`prepareXHome`/`Cleanup` are **0%** for codex, gemini, qwen, opencode. Only `claude.go` has real subprocess-lifecycle coverage. No provider tests the context-cancel→kill or timeout branch. `opencode` has zero parser tests; `cli.go`, `mcp.go`, `parse.go` have no test files.
- **Dispatcher:** `copyAttachmentsToWorktree`/`copyFile` **0%** — user uploads reaching the agent's worktree are untested. `worktree.go`'s `PushBranch` is **0%**, and it writes to user repos.
- **API:** `handlers/chat.go` has no test — a second WS auth surface (`Terminal`, line 136-141) entirely uncovered. `task_uploads.go`/`uploads.go` MIME sniffing, size cap, and the `isSafePathComponent` traversal guard are never called by a test. `ws/client.go`'s `ServeWS` (ticket vs. deprecated `?token=` fallback, origin CORS, 100-subscription cap) has no test file.
- **Security branches:** `withinBaseDir`'s symlink-resolution path (`repos.go:333-344`) and `isValidGitRef` have no direct tests.
- **Frontend:** `stores/tasks.ts` — the central board store — is untested, as are `workflow.ts`, `repos.ts`, `agents.ts`. No tests for `WorkflowPage` (391 lines), `FileDiffViewer` (477), `DiffReviewPane`, `RunLogPane`/`useRunLogs` (the most algorithmically dense hook in the app), `ReposPage`, `TemplatesPage`, `DashboardPage`, `AgentConfigPage`, `ProviderConfigPage`, `UsagePage`, `AgentPerformancePage`.
- **Migrations:** all 43 up/down pairs exist; **none are ever executed** beyond the 039/040 spot tests. No `up → down → up` round-trip in CI.

### 4d. Missing test infrastructure (the root cause, not the symptom)
- No injectable `git` seam (`worktree.go:276` shells out directly) → can't simulate a hang.
- No clock injection in the dispatcher, though `logretention`/`backup` already do this → retry-timing and stuck-sweep tests are awkward.
- No fake-binary harness for codex/gemini/qwen/opencode — `claude_test.go`'s `TestMain` re-exec trick should be generalized.
- **No `testdata/` fixtures anywhere in the provider package.** Parser fixtures are hand-written synthetic JSON for codex/gemini/opencode; only one claude/qwen fixture is a real capture. Ironically, `cli.go:31-68` already implements `AGENT_RAW_LOG_DIR` raw-dump capture — the infrastructure to generate real fixtures exists but nothing persists its output.
- Coverage is measured and uploaded but **never gated** — it can erode silently.

### 4e. Where the tests are genuinely good
Worth preserving as the model: the `agent` e2e suite drives the real Dispatcher+Pool+Engine
over goroutines against temp SQLite and temp git repos and asserts real state-machine
outcomes (`dispatch_e2e_test.go`, `subtasks_e2e_test.go`, `cost_budget_e2e_test.go`).
`pricing_test.go` covers exact/prefix/unknown→0 thoroughly. `middleware/auth_test.go`
includes a *named regression test for a prior vulnerability*
(`TestBearerAuth_WebSocketUpgrade_DoesNotBypass`) — replicate that convention for every
security fix in this report. CI runs `-race`.

---

## 5. Feature opportunities (ranked)

1. **Notification channel (Slack / email / webhook) for human-intervention events — M.** Grep of `backend/internal` for slack/webhook/smtp returns nothing; the only alerting is opt-in *browser* push, which requires an open tab with permission granted. For a pipeline meant to run unattended overnight, this is the single biggest gap between demo and trustworthy tool.
2. **Mid-run cost kill switch — M/L.** Budgets only gate the *next* dispatch; one runaway run can blow past the cap. `docs/agents.md` already acknowledges this. (Fix C4 first — budgets don't work at all for 3 providers.)
3. **Per-repo concurrency limits — S/M.** `maxWorkers` is one global pool; a single busy repo starves every other repo. Pairs naturally with the C3 lock fix.
4. **Orphan-worktree sweeper — S.** Mirrors the existing logretention/backup scheduler pattern; reclaims the archived-task leak noted above.
5. **Multi-user accounts / RBAC — L.** Auth is a flat set of named bearer tokens used only for audit attribution; any token holder is a full admin (edit workflows, delete repos, run shell commands). No `role`/`permission` anywhere in migrations. Fine solo, a blocker for teams.
6. **Task-level discussion thread — S.** Humans can only comment on diff lines; two people can't leave a general note on a task.
7. **Onboarding checklist / first-run wizard — S/M.** Guidance lives entirely in README prose; Health shows readiness checks but nothing walks a new user through repo → provider → agent → task. No onboard/wizard/welcome in the frontend.
8. **WIP limits per column — S**, and **duplicate/clone task — S** (templates cover recurring work, but not one-off variants).

**API exists but no UI:** budget early-warning (only fires at 100%), the hardcoded
`agent-in-progress` writeback label, and the named-token WS gap (legacy `?token=` only
accepts the single shared `API_TOKEN`, so WS clients can't be attributed to a named actor).

**Docs drift:** README's "Features at a Glance" omits the Chat tab / interactive PTY
terminal entirely, though `docs/board-mcp.md` documents it fully. `frontend/src/api/AGENTS.md`
claims strict mode is on; it isn't. Otherwise `docs/api.md` matches `router.go` closely.

---

## 6. Suggested sequencing

**Week 1 — secrets & silent failure.** C1 + H1 (redaction, rewriting `providers_test.go:78`),
C2 (shared scan helper + `scanner.Err()`), C4 (wire codex/gemini cost + `CostUnknown`).
All three are small diffs with outsized impact, and each needs the regression test written
with it.

**Week 2 — dispatch integrity.** C3 (scope the lock, guard `MoveLabel`) and C5 (timeout-wrap
git), plus the concurrency test that races a human transition against a live run. Add
`restart: unless-stopped` to backend (H7) and a real `/readyz` (H8) — both one-liners.

**Week 3 — hardening & perf.** H2 env allowlist, H3 prompt delimiters, H4/H5 (drop the
single-connection cap, scope the rollup queries to the requested page), the missing
`created_at` index, and frontend code splitting.

**Ongoing — close the test structural gap.** Generalize the `claude_test.go` fake-binary
harness to the other four providers; adopt the `useHumanNeededNotifications` capture
pattern for all WS-driven UI state; persist `AGENT_RAW_LOG_DIR` output into `testdata/`;
add a migration `up → down → up` job; gate coverage at its current floor so it can't erode.
