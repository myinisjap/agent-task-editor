# internal/agent

The agent package owns the agent runtime core: the provider abstraction, the bounded worker pool, and the dispatcher. Concrete provider backends (claude, anthropic, llm, opencode, qwen_code, codex_cli) live in the sibling `providers` package (see `providers/AGENTS.md`) — this package defines only the `Provider` interface and the shared types/errors providers are built against; it does **not** import `providers` (providers imports `agent`, never the reverse).

## Files

| File | Purpose |
|---|---|
| `provider.go` | `Provider` interface, `RunInput`, `Result`, `LogEntry`, `LogEntryType`, `TransitionHint`, `ReviewComment`, `SourceComment`, `AgentConfig`, `Task` — the shared types every provider is built against |
| `pool.go` | `Pool` — bounded goroutine pool; persists logs, publishes WS events; classifies transient vs genuine failures and drives the per-task retry budget |
| `dispatcher.go` | `Dispatcher` — periodic DB sweep; matches tasks to configs; submits jobs |
| `worktree.go` | Per-task git worktree provisioning, safety-net commit, diff, push, teardown; `RepoGitLock` (per-repo git serialization) |
| `subtasks.go` | `SubtaskCoordinator` — child→parent branch merge-back, conflict flagging, parent auto-advance (Mechanism 2, issue #82) |
| `terminal.go` | `TerminalManager`/`NewTerminalManager` — interactive chat session CLI process management |
| `errors.go` | `ErrTransient` — marks an error as a transient infra problem rather than a genuine task failure; `ErrMaxTurns` and `ErrCostBudgetExceeded` — typed, non-transient escalation signals (turn cap / mid-run cost-budget kill) that `pool.go#handleProviderError` detects via `errors.As` and routes to `waiting_human` instead of retrying |
| `errclass.go` | `Classification` (`genuine`/`transient`/`rate_limit`/`auth`, plus the structurally-detected `max_turns`/`cost_budget`) + `ClassifyLine` — the single source of truth for the string patterns that classify provider output. Most patterns are plain substrings, but the short ones (`429`/`502`/`503`/`504`/`eof`/`timeout`) are anchored regexps (word boundary and, for the 3-digit HTTP codes, an HTTP-status-ish context) so they don't false-positive on ordinary agent output like a diff hunk header (`@@ -429,7 +429,9 @@`) or a `typeof` in source code (issue #335). `providers.is429Line`/`providers.isTransientLine` (in the `providers` package) are thin wrappers over this; `providers.classifyResultMessage` prefers the claude/qwen stream-json typed `result` event's `api_error_status` field / text over raw line sniffing |
| `ratelimit.go` | `ErrRateLimit`, `RateLimitRegistry` (per-config 429 blocking), `BackoffDuration(WithBase)` exponential-backoff helpers; `UnblockIfNotBlockedSince` is the in-run-safe clear (see issue #344) — `Unblock` remains the unconditional variant |

Concrete runners (`ClaudeRunner`, `AnthropicRunner`, `LLMRunner`, `QwenRunner`, `CodexRunner`, `OpencodeRunner`) are constructed only in `backend/cmd/server/main.go`'s `providerFactory`, which imports both this package (for `agent.AgentConfig`/`agent.Provider`) and `providers` (for the concrete runner types).

## Branch-per-task / Worktrees

Each task works in its own git worktree on its own branch so concurrent agents on
the same repo don't conflict. The agent's `RepoPath` (`cmd.Dir`) is the worktree,
never the main clone.

- **Provision** (`dispatcher.go`): first dispatch calls `provisionWorktree` →
  `git worktree add -b ate-<slug>-<id8> <repo>/.ate-worktrees/<id> <baseRef>`.
  Branch/path/base are stored on the task (`SetTaskWorktree`) and reused on every
  re-run (feedback runs stack commits on the same branch).
- **Base ref**: `origin/HEAD` → `origin/main` → current `HEAD`, frozen on the task
  at provision so the diff stays stable if the default branch later moves.
- **Safety-net commit** (`pool.go`): after a `completed` run, `commitIfDirty`
  commits anything the agent left uncommitted. Agents may also commit themselves.
- **Diff** (`GET /tasks/{id}/diff`): `git diff merge-base(base, branch)..branch`.
- **Terminal label** (`engine.OnTerminal`, wired in `main.go`): push the branch to
  origin if the repo has a remote, then remove the worktree. The branch itself is
  kept at this point so it remains available for review (diffing, manual
  inspection) even after the worktree is gone.
- **Task delete** (`tasks.go`): removes the worktree; branch kept.
- **Post-merge cleanup** (`ghsync.Syncer`, `worktree.go#DeleteLocalBranch`): the
  background GitHub sync polls open PRs and, once it observes a task's PR has
  been merged (`git_state` transitions to `pr_merged`), removes any leftover
  worktree and force-deletes the task's *local* branch from the main clone. This
  is the only place local branches get cleaned up automatically — closed-without-
  merge PRs are left untouched so a human can still inspect/reopen them. Only the
  local branch is deleted; any remote branch (e.g. on `origin`) is left as-is.

## Provider Interface

```go
type Provider interface {
    Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error)
}
```

Providers stream log entries on `logCh` as they run. The pool drains this channel, persists entries in batches, and publishes them to the WS hub.

## Result Status Values

- `completed` — agent finished; `NextLabel` optionally specifies where to move the task
- `failed` — something went wrong; task stays on current label. What happens next depends on *why* it failed:
  - **Genuine failure** (the agent ran and the work itself failed, e.g. a plain `Result{Status:"failed"}` with no underlying transport/provider error): re-dispatch is immediate and unbounded, same as before this feature — the next 5s sweep picks the task straight back up.
  - **Transient infra failure** (rate limit, network blip, upstream 5xx, ambiguous timeout — see "Retry Policy" below): auto-retried up to `AgentConfig.MaxRetries` times with exponential backoff, then escalated to `waiting_human` so a human doesn't have to guess whether it's quietly retrying or stuck.
- `waiting_human` — agent called `request_human`, hit a login/auth error, or exhausted its transient-retry budget; `Message` surfaces to the UI
- `cancelled` — a human stopped the run via `POST /tasks/{id}/runs/{run_id}/cancel` (see "Run Cancellation" below). Terminal; excluded from usage/dashboard aggregates.

## Run Cancellation (Kill Switch)

`Pool.Cancel(runID)` stops an in-flight run. The pool keeps a per-run cancel
registry (`running map[string]*runControl`, guarded by `mu`): `run()` derives a
cancellable context from the worker context, registers its `cancel` func before
invoking the provider, and unregisters on return. `Cancel` flips the run's
`cancelled` flag and calls `cancel()`; because the provider runs under this
context, CLI providers' `exec.CommandContext` subprocesses are killed and HTTP
providers abort their request.

When the provider returns, `run()` checks the `cancelled` flag **before** error
classification (a cancelled provider usually surfaces a context/transient-looking
error) and routes to `handleCancelled`, which:

- marks the run `cancelled` with a note (does **not** count as failure);
- resets the task's transient-retry budget (a cancel consumes none);
- **pauses the task** and clears `active_agent_run_id`. Pausing is deliberate:
  clearing the lock alone would let the next 5s sweep re-dispatch the very run
  just killed. Pausing leaves the task on its label for a human to resume.
- publishes `task.agent_done` (status `cancelled`) plus `task.updated` so boards
  not subscribed to the task still refresh the paused state.

The HTTP handler (`TasksHandler.CancelRun`) only signals — it returns `202` once
`Cancel` succeeds, `409` if the run isn't `running` (or is no longer registered,
e.g. it finished in the race window), and `404` if the run doesn't belong to the
task. The DB writes and WS broadcast happen asynchronously in the pool goroutine.

## Retry Policy (Transient vs Genuine Failures)

Per-`AgentConfig` fields `max_retries` (default 3, 0 disables auto-retry) and
`retry_backoff_secs` (default 30, base for exponential backoff capped at 10m)
govern automatic retries for **transient** provider errors only:

- **Classification** (`errclass.go`, `errors.go`, `ratelimit.go`): every failure
  resolves to one explicit `Classification` — `genuine`, `transient`,
  `rate_limit`, or `auth` — logged as the `classification` field on the failure
  log line so misclassifications are diagnosable from logs alone. Any error
  implementing `Transient() bool` (both `ErrRateLimit` and `ErrTransient`) is
  treated as transient. HTTP providers (`providers/anthropic.go`, `providers/llm.go`) wrap
  network-level `Do()` errors and `5xx` responses as `ErrTransient`; `429` stays
  `ErrRateLimit`. CLI providers (`providers/claude.go`, `providers/qwen.go`, `providers/opencode.go`,
  `providers/codex.go`) classify stdout/stderr via the **single** pattern
  table in `errclass.go` (`ClassifyLine`) — connection resets, `502/503/504`,
  "timeout", `429`/rate limit, and "Not logged in"/"Please run /login" all live
  in that one table with per-pattern unit tests, so a CLI-wording change is a
  one-line edit. The short patterns (`429`/`502`/`503`/`504`/`eof`/`timeout`)
  are anchored (word boundary, plus an HTTP-status-ish context for the
  3-digit codes) rather than bare substrings, since they're short enough to
  otherwise appear in ordinary agent prose or file contents (issue #335).
  Raw-line sniffing on **stdout** is applied only to lines that failed to
  parse as a structured event (`streamEvent.Parsed` for claude/qwen,
  `classifyCodexJSON`'s `parsedJSON` return for codex, `classifyOpencodeJSON`'s
  `parsedJSON` return for opencode) — a successfully-parsed
  assistant/tool_use/tool_result/etc. event has already been classified (or
  deliberately left `ClassNone`) by its typed path, so re-sniffing its
  Content (the agent's own prose or a file it read/wrote) is pure
  false-positive surface. **Stderr** is always sniffed regardless, since it's
  untyped diagnostic output. For the claude/qwen providers, the typed
  stream-json `result` event (`providers/parse_streamjson.go`'s
  `classifyResultMessage`) is preferred over raw line sniffing where
  present; `providers/parse_codex.go` has its own dedicated
  `classifyCodexJSON` parser instead, since Codex's
  JSON event schema is not compatible with claude/qwen's stream-json envelope, but
  it still prefers its own typed terminal event's classification over raw
  line sniffing the same way.
  An ambiguous run-timeout (context deadline exceeded) is also treated as
  transient without needing a log signal. A plain non-zero CLI exit with no such
  signal, or a `Result{Status:"failed"}` with no error at all, is a **genuine**
  failure and does not consume retry budget. An `auth` signal in the run's logs
  (login/auth failure) escalates to `waiting_human` instead of retrying.
- **Budget tracking** (`tasks.transient_retry_count`, `tasks.next_retry_at`):
  `pool.go#handleTransientFailure` increments the task's counter and sets
  `next_retry_at` (via `BackoffDurationWithBase(count, RetryBackoffSecs)`)
  when under budget, clearing the active-run lock so the dispatcher can
  re-pick it once eligible. `ListAgentPickupTasks` filters out tasks whose
  `next_retry_at` is still in the future. Once the budget is exhausted the
  task escalates to `waiting_human` (and the counter resets, so a
  human-triggered re-dispatch starts a fresh budget). A successful run or a
  genuine (non-transient) failure also resets the counter to 0.
- **Complementary, not a replacement, for `RateLimitRegistry`**: a 429 both
  blocks the *whole agent config* for a backed-off period (existing
  behavior, unrelated to any specific task) **and** consumes that task's
  transient-retry budget — the two mechanisms operate independently on
  different scopes (config-wide throttle vs per-task retry cap). With
  `MAX_WORKERS > 1`, several runs can share one agent config; the pool's
  post-run cleanup clears a config's rate-limit block via
  `UnblockIfNotBlockedSince(cfgID, startedAt)` rather than the unconditional
  `Unblock`, so a run that started before a sibling run's fresh 429 can't wipe
  that block (or reset the consecutive-429 counter that drives
  `BlockWithBackoff`'s escalating ladder) out from under it — see issue #344.

## Cost Budgets (Pre-Dispatch Guard + Mid-Run Kill Switch)

`AgentConfig.MaxCostUSD` / task `max_cost_usd` (`effectiveBudget`,
`dispatcher.go`, picks the lower nonzero of the two) is enforced two ways —
see `docs/agents.md#cost-budgets` for the user-facing writeup:

- **Pre-dispatch guard** (`Dispatcher.checkCostBudget`, all providers): sums
  `SumTaskCost` across every run for the task (any status) before each
  sweep-dispatch; if `spent >= budget`, creates a phantom `waiting_human` run
  (no provider invocation), locks the task, and publishes `task.needs_human`
  (`budget exhausted: $<spent> of $<budget>`). Never touches an in-flight run
  — this is the *only* enforcement for providers without watchdog support.
- **Mid-run kill switch** (providers with mid-run priced usage only —
  currently `claude` and `qwen_code` when its model is priced; see
  `providers/cost_watchdog.go` and `providers/AGENTS.md`): the provider
  itself watches incremental token usage as the run streams, projects total
  cost via the pricing table, and cancels its own subprocess plus returns
  `&ErrCostBudgetExceeded{SpentUSD, BudgetUSD}` once the projection crosses
  `RunInput.CostBudgetUSD`. `pool.go#handleProviderError` detects this via
  `errors.As` (checked before the transient-error branch, same as
  `ErrMaxTurns`) and calls `pool_failure.go#handleCostBudgetExceeded`, which
  is modeled directly on `handleMaxTurnsExhausted`: marks the run
  `waiting_human` with notes `mid-run cost budget exceeded: $<spent> of
  $<budget>`, leaves the task locked, publishes `task.needs_human` +
  `task.agent_done`. Never retried — re-dispatching would just spend against
  the same already-exhausted budget again. A killed run never reaches its
  terminal `result` event, so `applyUsage` (which normally reads token/cost
  usage from that event) has nothing to read — `claude.go`/`qwen.go` instead
  populate `Result.InputTokens`/`OutputTokens`/`CostUSD` from the watchdog's
  own cumulative-usage snapshot at the moment it cancelled (this run's own
  incremental cost = projected total minus `RunInput.CostSpentUSD`, so prior
  runs' cost isn't double-counted). `handleCostBudgetExceeded` persists these
  onto the run row exactly like every other terminal path, so `SumTaskCost`
  doesn't undercount a killed run and a kill→raise-budget→resume cycle can't
  silently reset the task's cost ledger.
- **Early warning**: independent of both guards above, a `task.cost_warning`
  WS event fires once cumulative/projected spend crosses a global, DB-backed
  `warn_ratio` threshold (default 0.8; `Dispatcher.resolveCostWarnRatio`,
  `GET`/`PUT /api/v1/settings/cost-warning`) — from the mid-run watchdog
  (`Result.CostWarned`, surfaced by `pool.go#run`) or, for any provider, from
  `checkCostBudget` itself when a task is already past the threshold at
  dispatch time (gated by the task's one-shot `cost_warned` column so it
  doesn't refire every sweep; reset when the task's own `max_cost_usd`
  changes via `UpdateTask`).
- **`RunInput.CostBudgetUSD`/`CostSpentUSD`/`CostWarnRatio`** are populated
  in `Dispatcher.startRun` and are the only channel through which a provider
  learns about the budget — providers never query storage directly.

## Session Resume & Reply-to-Agent

Each `claude`/`qwen_code` run's stream-json envelope carries a `session_id`;
`classifyStreamJSON` extracts it, the Result carries it, and the pool persists
it (`SetAgentRunSession`) on any outcome. `codex_cli` runs record
a thread id the same way (from its own
`classifyCodexJSON` parser), but no provider actually resumes it except
`claude`. `Dispatcher.startRun` looks up the
latest session for (task, agent config) via `GetLatestTaskSession` — gated on
`provider == "claude" && resume_sessions` — and sets `RunInput.ResumeSessionID`;
`providers/claude.go` then passes `--resume` with a **condensed prompt**
(`buildResumePrompt`: human reply + feedback + open review comments only, since
the resumed conversation already contains the task context). If the resume
target is gone (`isResumeErrorLine`, or an error exit with no stream output —
see `shouldFallBackToColdStart`), `Run` retries once cold.

`Dispatcher.DispatchReply(taskID, message)` is the reply-to-agent entrypoint
(`POST /tasks/{id}/runs/{run_id}/reply`): it validates the task's active run is
`waiting_human`, prefers that run's agent config, and starts a run with
`RunInput.HumanReply` set (rendered as `RESPONSE FROM HUMAN` in the prompt) and
the reply recorded as the new run's first log entry. The replied-to run keeps
its `waiting_human` status — same as the approve/reject flows — and the task's
active-run lock moves to the new run. `dispatch()` and `DispatchReply` share
`startRun` (worktree provisioning, atomic run creation, pool submit).

## Review Comment Feedback Loop

Humans leave persistent, file/line-anchored review comments on a task's diff
(`task_review_comments`, managed via `/tasks/{id}/review-comments`). Forge
(GitHub/Gitea) inline PR review comments land in the same table via
`ghsync.ingestReviewComments`, tagged with `source` — but only from an author
with write access to the repo (`OWNER`/`MEMBER`/`COLLABORATOR`); an author
without it is dropped entirely at ingestion time and never reaches this
table, because this whole section renders as trusted, "address every one"
content (see #331 and `docs/task-sources.md`'s "PR review / GitHub Actions
feedback ingestion" section). The dispatcher loads the task's **open**
comments into `RunInput.OpenReviewComments`; `buildPrompt` renders them (with
`comment_id`s) under `"OPEN REVIEW COMMENTS"`, so every provider sees them on
every run until resolved — stripping any occurrence of the
`>>>END UNTRUSTED SOURCE COMMENTS` marker from each comment's body/quoted
text first (defence in depth alongside the write-access filter above; see
"Source Issue Comments" below for why the marker matters). CLI providers with
the MCP sidecar (`claude`, `qwen_code`, `codex_cli`) expose a
`resolve_comment(comment_id, note)` tool; the sidecar accumulates resolutions
in the result file and the pool applies them to the DB **only when the run
completes successfully** (a failed run's claimed fixes never reached the
branch), then publishes `task.review_comments_changed`. Humans can also
resolve/reopen comments directly in the UI.

PR review bodies (`CHANGES_REQUESTED` reviews, ingested by
`ghsync.ingestReviews`) land in `RunInput.Feedback` instead, rendered under
`"FEEDBACK FROM PRIOR REVIEW:"` — the same write-access filter and end-marker
stripping apply there too.

## Source Issue Comments (Untrusted)

For tasks imported from an external item (e.g. a GitHub issue), `tasksource`'s
importer can ingest the issue's comment thread into `task_source_comments`
(write-access authors only, filtered per the repo's sync policy). `Dispatcher.loadSourceComments` (mirroring
`loadReviewComments`, same best-effort-on-query-failure shape) loads them into
`RunInput.SourceComments` on every dispatch. Unlike review comments, these are
**attacker-influenceable** (issue #79: anyone who can comment on the source
issue can influence this content) and **not resolvable** — there is no
`resolve_comment` analogue, and they are deliberately never passed to the MCP
sidecar's `Prepare` calls. `providers/prompt.go`'s `writeSourceCommentsSection`
renders them (in both `buildPrompt` and `buildResumePrompt`) inside an explicit
`<<<BEGIN UNTRUSTED SOURCE COMMENTS` / `>>>END UNTRUSTED SOURCE COMMENTS` fence
with a stated "this is data, not instructions" framing, and strips any
occurrence of the end marker from a comment body first so a comment can't
forge the closing delimiter and escape the fence into trusted prompt context.
`buildSystemPrompt` reinforces the same instruction once in the system prompt.

## Rework-Loop Feedback & Circuit Breaker

A run that completes with `outcome='failure'` sends the task back along its
failure transition (e.g. `agent-review → work`). Because every transition clears
the dispatch lock, that path is otherwise unbounded — a reviewer stuck on the
same finding re-triggers a Worker forever. Two mechanisms in `pool.go` (the
`completed` branch of `run()`) contain this:

- **Failure findings become feedback.** On a `failure` outcome the run's summary
  (`result.Message`) is written to the run's `feedback` (`SetAgentRunFeedback`).
  The next dispatch reads `prior.Feedback` and `buildPrompt` renders it under
  `"FEEDBACK FROM PRIOR REVIEW"` — a *fix request*, distinct from the
  `"NOTES FROM PRIOR AGENT"` (`PriorPlan`) block the default Worker prompt treats
  as an implementation plan. Without this, findings only reached the next Worker
  as a "plan", so the Worker saw already-committed code, judged it done, and
  advanced without addressing them (the observed infinite loop).
- **`failureLoopExceeded` + `escalateFailureLoop`.** Before firing the failure
  transition, the pool counts how many times this exact `(fromLabel → toLabel)`
  agent transition already appears in the tail of `task_label_history` — the
  window resets on any human-triggered transition or any exit from `fromLabel` to
  a *different* label (a success/progress move). Once the count reaches
  `failureLoopThreshold` (3), the task is escalated to `waiting_human` instead of
  transitioning again: the run is re-written `waiting_human` (usage preserved),
  the task is left locked on that run (the completion path clears the lock only
  in the branches that own that responsibility — see below — not before the
  escalation), and `task.needs_human` is published — mirroring the
  transient-retry and cost-budget escalations. This is history-derived (no new
  column), so it needs no migration and a human move naturally resets the budget.

## Dispatch / Active Run Locking

`active_agent_run_id` prevents double-dispatch:
- Dispatcher sets it when creating a run
- Pool clears it on `failed` / `cancelled`, and on a `completed` run that has no
  resolvable outcome or whose transition is rejected
- On a `completed` run that transitions, the pool does **not** pre-clear the
  lock — `engine.Transition`'s compare-and-swap clears `active_agent_run_id`
  atomically as part of the label move. Pre-clearing would open a window for the
  dispatcher to re-pick the task between the clear and the transition landing (a
  real double-dispatch race, widened by any DB work done in between such as the
  failure-feedback/loop-check above).
- Pool leaves it set on `waiting_human` (including the rework-loop escalation)
- `UpdateTaskLabel` (any workflow transition) always clears it via SQL

A task's `paused` flag (a persisted DB column, set via `PATCH
/tasks/{id}/pause`) is filtered out at the SQL level in
`ListAgentPickupTasks` (`AND t.paused = 0`), regardless of label or
`active_agent_run_id`. `dispatch()` also re-checks `t.Paused` as
defense-in-depth. Pausing does not cancel an already-running agent run; it
only prevents the dispatcher from starting a new one. The reverse also holds:
**cancelling** a run (see "Run Cancellation" above) pauses the task, so the
kill switch both stops the current run *and* blocks the immediate re-dispatch
that clearing the lock would otherwise trigger.

## Terminal Sessions (`terminal.go`)

`TerminalManager` runs one live PTY subprocess per interactive chat session, kept
alive across WebSocket disconnects (see the package-level doc comment on
`TerminalManager`). Two lifecycle details worth knowing when touching this file:

- **Owner-scoped session-map delete.** The output-pump goroutine (started at the
  end of `ensure()`) deletes its session from `m.sessions` only if the map still
  holds the *same* `*ptySession` it started with (`m.sessions[sessionID] == s`),
  not an unconditional `delete`. `Stop()`/`reapIdleOnce` may already have removed
  the entry before `cmd.Wait()` returns, and a reattach in that window inserts a
  *fresh* session under the same id — an unconditional delete would then orphan
  that new session (alive, but unreachable by `Stop`/`reapIdleOnce`, and no
  longer counted against `MaxSessions`). Same ownership-bug class and fix shape
  as `ClearActiveAgentRunIfOwner` above (#244), applied to a different map.
- **`Attach` closes the WS when the CLI process exits.** The read pump in
  `Attach` blocks in `conn.Read` until the client sends something, so if the CLI
  process exits while the user is idle (crash, `/exit`, auth expiry), nothing
  would otherwise wake it — the session is already gone from `m.sessions`, so the
  reaper can't reach it either, and both the handler goroutine and the WS
  connection would leak. `Attach` starts a small watcher goroutine (torn down via
  `watchDone` when `Attach` returns for any other reason) that closes the
  connection as soon as `s.done` fires, so `conn.Read` unblocks and the browser
  sees the session end instead of freezing silently.

## Subtask Merge-Back Coordinator (Mechanism 2)

`SubtaskCoordinator` (`subtasks.go`) owns the child→parent branch lifecycle for
agent-driven decomposition (issue #82). Wired in `cmd/server/main.go`:
`engine.OnTerminal` calls `OnChildTerminal` for a subtask (else the normal
push/teardown), `pool.Subtasks` calls `AfterParentRun` when a parent's run
finishes, and `dispatcher.Subtasks` injects merge-conflict context into a
parent's run prompt via `BuildConflictContext`.

- **Merge-back on child terminal:** the child's branch is merged into the
  parent's branch (`MergeBranch`, a `--no-ff` merge commit); on success the
  child's worktree + local branch are removed (children never push to origin). A
  conflict is aborted cleanly and the child flagged `merge_status=merge_conflict`.
- **Auto-advance** fires only when **every** non-archived child is terminal *and*
  `merge_status=merged` (not merely terminal — see the double-advance guard
  below). It moves the parent along its agent-success transition with the
  `workflow.TriggerSubtasksComplete` trigger, which bypasses the human/agent gate
  checks (the coordinator selects an already-validated agent-success target).
- **Concurrency:**
  - *Per-parent lock* (`plocks`): all merge-back + evaluate work for one parent
    runs under its mutex, so children finishing simultaneously merge one at a time
    in completion order and can't corrupt the parent worktree.
  - *Per-repo git lock* (`RepoGitLock`, `worktree.go`): every ref-mutating git
    call site takes the repo's lock around its git calls — the pool's
    safety-net commit/push, the coordinator's merge/teardown, the dispatcher's
    worktree provisioning (`ensureWorktree`'s `git fetch --prune` + `git
    worktree add -b`), `cmd/server/main.go`'s `engine.OnTerminal` (non-subtask
    branch push + worktree teardown), and the worktree sweeper's reclaim pass
    (`git worktree remove --force` / `git worktree prune`). Git worktrees
    share one ref store, so without this a commit in one worktree races a
    merge/branch-delete/provision in another ("cannot lock ref 'HEAD'"). Lock
    order is always parent-lock → repo-lock (nothing but the coordinator ever
    takes the parent lock), so there's no cycle. The mutex is plain and
    **not reentrant** — it's always taken at the call site around the git
    op(s), never inside the shared helpers themselves
    (`provisionWorktree(From)`, `RemoveWorktree`, `PushBranch`), since the
    coordinator already holds it across calls into those helpers; locking
    inside them would self-deadlock. See issue #344.
  - *Double-advance guard:* requiring `merged` (not just terminal) in
    `evaluateParent` means a sibling that is terminal-but-not-yet-merged (its
    merge queued behind the parent lock) does not trigger a premature advance;
    the advance happens exactly once, when the last merge lands.
- **Deferred merges:** if a parent has a run in flight when a child goes terminal,
  the merge is marked `pending` and flushed by `AfterParentRun` once the run ends.
- **Tested end-to-end:** `subtasks_e2e_test.go` drives the real
  dispatcher+pool+engine+coordinator over a temp git repo with a file-writing
  fake provider (two children branch off the parent, run to terminal, merge back
  concurrently, and the parent auto-advances). `subtasks_coord_test.go` unit-tests
  the clean-merge, conflict, and auto-advance paths directly.

## Environment Variable Security

`mergeEnv` (in `providers/cli.go`) blocks keys that could hijack the subprocess: `PATH`, `LD_PRELOAD`, `LD_LIBRARY_PATH`, `HOME`, `SHELL`, `IFS`, `DYLD_INSERT_LIBRARIES`, `DYLD_LIBRARY_PATH`. Blocked keys are logged as warnings, not silently dropped. `input.AgentConfig.Env` (the map `mergeEnv` merges in) now comes from the referenced `ProviderConfig`'s `env` JSON column rather than directly off `agent_configs` — `mergeEnv` itself is unaffected by that split, it only ever sees the already-resolved map.

## Adding a New Provider

1. Implement `Provider` in a new file in `providers/` (e.g. `providers/newprovider.go`)
2. Add a new case to `providerFactory` in `cmd/server/main.go`
3. Add the provider string to `knownProviders` in `internal/api/handlers/agents.go` (validated on both agent-config and provider-config create/update — see `internal/api/handlers/providers.go`)

`AgentConfig.Provider`/`.Model`/`.Env` (the fields providers actually read off
`input.AgentConfig`) are populated from the joined `ProviderConfig` — a
separate, reusable entity (`GET/POST /api/v1/provider-configs`) that both an
`agent_configs` row and a `chat_sessions` row reference by
`provider_config_id`, rather than each inlining its own provider/model/env.
See [docs/agents.md § Provider Configs](../../../docs/agents.md#provider-configs).
`toAgentConfig` (`dispatcher.go`) resolves a `ProviderConfig` and populates the
flat `AgentConfig.Provider`/`.Model`/`.Env` fields for task runs; interactive
chat resolves the session's `ProviderConfig` in `internal/api/handlers/chat.go`
and passes the provider/model strings to `TerminalManager.Attach`. A new
provider implementation never needs to know about this split, since it only
ever reads those three fields off `input.AgentConfig` the same way it always
has.

## Logging Conventions

This package uses stdlib `log/slog` exclusively (no third-party logging
libraries). Every log line carries a `component` field (`"dispatcher"` or
`"pool"`) plus whichever of `task_id`/`run_id` are known at that point, so
logs for a given task/run can be grepped/aggregated across both the
dispatcher and the pool.

Rather than repeating `"component", "dispatcher", "task_id", t.ID, ...` on
every call, build a scoped logger once with `slog.With(...)` at the top of a
function (or as soon as the relevant ID becomes known) and reuse it:

```go
log := slog.With("component", "dispatcher", "task_id", t.ID)
...
runID := uuid.NewString()
log = log.With("run_id", runID) // rebind once run_id is known
...
log.Info("dispatcher: agent dispatched", "label", t.Label)
```

- `dispatch()` and `run()` (pool) build their scoped logger at the top of the
  function; `resolveOutcome()` and `persistLogs()` do the same for the fields
  they have available.
- Sweep-level logs (before a task is picked) and other call sites without a
  task/run in scope use the package-level `slog.Xxx(...)` functions directly
  with an explicit `"component"` field.
- Keep the existing `"dispatcher: ..."` / `"pool: ..."` message-string
  prefixes — they're a codebase-wide convention (see also `ghsync`) and
  should not be removed when consolidating fields.
