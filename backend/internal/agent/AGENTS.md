# internal/agent

The agent package owns the agent runtime core: the provider abstraction, the bounded worker pool, and the dispatcher. Concrete provider backends (claude, opencode, qwen_code, codex_cli) live in the sibling `providers` package (see `providers/AGENTS.md`) — this package defines only the `Provider` interface and the shared types/errors providers are built against; it does **not** import `providers` (providers imports `agent`, never the reverse).

## Files

| File | Purpose |
|---|---|
| `provider.go` | `Provider` interface, `RunInput`, `Result`, `LogEntry`, `LogEntryType`, `TransitionHint`, `ReviewComment`, `SourceComment`, `AgentConfig`, `Task` — the shared types every provider is built against |
| `pool.go` | `Pool` — bounded goroutine pool; persists logs, publishes WS events; classifies transient vs genuine failures and drives the per-task retry budget |
| `dispatcher.go` | `Dispatcher` — periodic DB sweep; matches tasks to configs; submits jobs |
| `worktree.go` | Per-task git worktree provisioning, safety-net commit, diff, push, teardown; `RepoGitLock` (per-repo git serialization) |
| `subtasks.go` | `SubtaskCoordinator` — child→parent branch merge-back, conflict flagging, parent auto-advance (ADR 0016) |
| `terminal.go` | `TerminalManager`/`NewTerminalManager` — interactive chat session CLI process management |
| `errors.go` | `ErrTransient`; typed non-transient escalation signals `ErrMaxTurns` and `ErrCostBudgetExceeded` (detected via `errors.As` in `pool.go#handleProviderError`, routed to `waiting_human`, never retried) |
| `errclass.go` | `Classification` + `ClassifyLine` — the single table of patterns that classify provider output (ADR 0010). `providers.is429Line`/`isTransientLine` are thin wrappers |
| `ratelimit.go` | `ErrRateLimit`, `RateLimitRegistry` (per-config 429 blocking; write-through to `agent_rate_limits` via an optional `RateLimitStore`, reloaded at boot, nil store = in-memory), `BackoffDuration(WithBase)`; use `UnblockIfNotBlockedSince` (not `Unblock`) for in-run clears (ADR 0011) |

Concrete runners (`ClaudeRunner`, `QwenRunner`, `CodexRunner`, `OpencodeRunner`) are constructed only in `backend/cmd/server/main.go`'s `providerFactory`, which imports both this package (for `agent.AgentConfig`/`agent.Provider`) and `providers` (for the concrete runner types).

## Worktrees (ADR 0008, 0009)

Each task runs in its own worktree/branch (`<repo>/.ate-worktrees/<id>`, branch `ate-<slug>-<id8>`); `RepoPath`/`cmd.Dir` is the worktree, never the main clone. Provisioned on first dispatch, stored via `SetTaskWorktree`, reused on every re-run, base ref frozen at provision.

- Safety-net `commitIfDirty` runs after a `completed` run; `GET /tasks/{id}/diff` is `merge-base(base, branch)..branch`.
- `engine.OnTerminal` pushes the branch (if the repo has a remote) and removes the worktree; the branch is kept.
- Task delete: 409 while a run is live, else remove the worktree under `RepoGitLock`.
- Local branches are deleted in exactly one place: `ghsync` after it observes `pr_merged`. Never delete remote branches or closed-unmerged PR branches.
- **Every ref-mutating git call site takes `RepoGitLock` around its git calls; the helpers (`provisionWorktree(From)`, `RemoveWorktree`, `PushBranch`) must not lock inside.** The mutex is not reentrant. Lock order is parent lock → repo lock.

## Provider Interface and Result Statuses

```go
type Provider interface {
    Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error)
}
```

Providers stream `LogEntry` on `logCh`; the pool persists in batches and publishes to the WS hub. Result statuses:

- `completed` — `NextLabel` optionally names where the task moves.
- `failed` — genuine failures release the lock and the next sweep re-picks the task, up to `maxConsecutiveFailures` (3, `tasks.consecutive_failure_count`), then `waiting_human`. Transient failures are retried per the Retry Policy below.
- `waiting_human` — `request_human`, auth error, exhausted retry budget, max turns, cost kill, or a rework-loop escalation. Keeps the lock (ADR 0002); `Message` surfaces to the UI.
- `cancelled` — a human stopped the run. Not a failure; excluded from usage aggregates.
- `interrupted` — shutdown or crash mid-run. Not a failure: no counters touched, task not paused, lock released, session id kept (ADR 0026). `Pool.Shutdown(drain)` stops `Submit`, interrupts queued jobs, waits, then cancels stragglers with `CancelReasonShutdown`.

## Run Cancellation (ADR 0012)

`Pool.Cancel(runID)` cancels the run's context via the per-run registry. `run()` checks the `cancelled` flag **before** error classification and calls `handleCancelled`: run → `cancelled`, retry budget reset, **task paused**, lock cleared, `task.agent_done` + `task.updated` published. `TasksHandler.CancelRun` only signals (202 / 409 not running or unregistered / 404 wrong task). Keep the pause: without it the next sweep re-dispatches the killed run.

## Retry Policy (ADR 0010, 0011)

- Every failure resolves to one `Classification` (`genuine`/`transient`/`rate_limit`/`auth`, plus `max_turns`/`cost_budget`), logged as `classification`. Add or change patterns only in `errclass.go`'s table, with a unit test; keep short patterns anchored.
- Errors implementing `Transient() bool` are transient. HTTP providers wrap network errors and 5xx as `ErrTransient`, 429 as `ErrRateLimit`. CLI providers classify via `ClassifyLine`, sniffing stdout **only** for lines that did not parse as a structured event (`streamEvent.Parsed` / `parsedJSON`); stderr is always sniffed. Prefer typed result events over sniffing.
- Context-deadline timeout = transient. Plain non-zero exit with no signal = genuine. `auth` escalates to `waiting_human` immediately.
- Transient budget: `AgentConfig.MaxRetries` (default 3; 0 escalates immediately, lock kept) and `retry_backoff_secs` (default 30, cap 10m). `tasks.transient_retry_count` / `next_retry_at`; `ListAgentPickupTasks` hides tasks still backing off. Exhaustion → `waiting_human` and counter reset. Success or a genuine failure resets the transient counter; transient failures never reset the genuine counter.
- A run completing with no resolvable outcome escalates once via `escalateNoOutcome` (lock kept, single `task.needs_human`).
- A 429 / usage limit blocks the whole agent config through `RateLimitRegistry` and goes through `handleRateLimited`, which releases the task **without** spending its transient budget (the dispatcher fails over to the next config by priority or waits for the block). Only with a nil registry does it fall back to the task budget.

## Cost Budgets (ADR 0013)

Budget = lower nonzero of `AgentConfig.MaxCostUSD` and task `max_cost_usd` (`effectiveBudget`). User docs: `docs/agents.md#cost-budgets`.

- `Dispatcher.checkCostBudget` (all providers): if `SumTaskCost` ≥ budget, create a phantom `waiting_human` run (no provider call), lock, publish `task.needs_human`. Never touches an in-flight run.
- Mid-run kill (`claude` and priced `qwen_code` only): the provider's `costWatchdog` cancels its subprocess and returns `*ErrCostBudgetExceeded`; `handleCostBudgetExceeded` → `waiting_human`, never retried. The provider must populate `Result.InputTokens/OutputTokens/CostUSD` from the watchdog snapshot (this run's increment = projected total − `RunInput.CostSpentUSD`) so `SumTaskCost` doesn't undercount.
- `task.cost_warning` fires once past the DB-backed `warn_ratio` (default 0.8); the per-task `cost_warned` column makes it one-shot.
- Providers learn the budget only via `RunInput.CostBudgetUSD/CostSpentUSD/CostWarnRatio` (set in `startRun`); they never query storage.

## Session Resume & Reply-to-Agent (ADR 0028)

The pool persists each run's provider session id (`SetAgentRunSession`). `startRun` → `resolveAgentConfig` sets `RunInput.ResumeSessionID` for any provider `providerSupportsResume` lists (`claude`, `qwen_code`, `codex_cli`, `opencode`) with `resume_sessions` on (via `GetLatestTaskSession`); each runner then uses its own resume flag (`claude`/`qwen_code` `--resume`, `codex_cli` `exec resume`, `opencode` `--session`) with `buildResumePrompt` (reply + feedback + open review comments only, not the full task context — already in the resumed conversation) and cold-starts once (full `buildPrompt`) if the target session no longer exists: `claude`/`qwen_code`/`opencode` detect this via `isResumeErrorLine` (text-matched "no conversation found" / "session ... not found" on stdout+stderr) or an error exit with no stream output at all (`shouldFallBackToColdStart` in claude.go and its per-provider mirrors `qwenShouldFallBackToColdStart`/`opencodeShouldFallBackToColdStart`); `codex_cli` matches its own distinct stderr wording (`codexMissingRolloutMarker`, "no rollout found for thread id") in codex.go. `Dispatcher.DispatchReply(taskID, message)` requires the task's active run to be `waiting_human`, starts a run with `RunInput.HumanReply` (rendered as `RESPONSE FROM HUMAN`), and moves the lock to the new run; the replied-to run keeps `waiting_human`. `dispatch()` and `DispatchReply` share `startRun`.

## Review Comments, Source Comments, Feedback (ADR 0014, 0015)

- Open `task_review_comments` go to `RunInput.OpenReviewComments`, rendered under `OPEN REVIEW COMMENTS`. Agents resolve them with the sidecar's `resolve_comment`; the pool applies resolutions **only when the run completes successfully** and publishes `task.review_comments_changed`. Forge comments and reviews are ingested only from authors with write access (`OWNER`/`MEMBER`/`COLLABORATOR`). PR review bodies land in `RunInput.Feedback` (`FEEDBACK FROM PRIOR REVIEW:`).
- Source issue comments (`task_source_comments`, loaded by `Dispatcher.loadSourceComments`) are **untrusted and not resolvable**: never pass them to the sidecar's `Prepare`; render only through `writeSourceCommentsSection` inside the `UNTRUSTED SOURCE COMMENTS` fence (in both `buildPrompt` and `buildResumePrompt`). Every third-party string in a prompt must go through `stripSourceCommentsMarker`.
- A `failure` outcome stores the run summary as the run's `feedback` (`SetAgentRunFeedback`), rendered next dispatch as `FEEDBACK FROM PRIOR REVIEW` (not as `PriorPlan`). `failureLoopExceeded`/`escalateFailureLoop` escalate to `waiting_human` after `failureLoopThreshold` (3) identical `(from → to)` agent transitions in `task_label_history`; history-derived, no column.

## Verify Command (ADR 0038)

`verify.go`: for a `completed` + `success` run, `Pool.verifyOutcome` runs the repo's (or the label's, `verify_mode`) command through the `Verifier` seam (`providers.CommandVerifier`; nil verifier = fail closed) before `resolveOutcome`. Fail/timeout rewrites the outcome to `failure` and stores `VERIFICATION FAILED:` feedback (`Result.verify`, pool-internal); no failure path = `holdOnVerifyFailure`; `error` = escalate. Authoritative: never let a success transition through on a non-pass. The command is untrusted repo code, so it must keep going through `newCLICommand`/`agentuser` + `applyRuntime`, never raw exec.

## Dispatch / Active Run Locking (ADR 0001–0003)

- Claim via `ClaimTaskActiveRunIfFree` (sweep) / `ClaimTaskActiveRunIfHeldBy` (reply) inside `persistRunRow`'s transaction; zero rows → `ErrDispatchConflict` (sweep skips, reply handler 409). `SetTaskActiveRun`/`SetTaskActiveRunOnly` are only for escalation bookkeeping on a lock the run already owns.
- Pool clears the lock on `failed` / `cancelled` and on a `completed` run with no resolvable outcome or a rejected transition, always with `ClearActiveAgentRunIfOwner`. It leaves it set on `waiting_human`.
- On a `completed` run that transitions, **do not pre-clear the lock**; `engine.Transition`'s CAS clears it atomically.
- `paused` and `archived` are filtered in SQL by `ListAgentPickupTasks`; `dispatch()` re-checks `Paused`. Pausing doesn't cancel a running run. Cancelling pauses (see above).

## Terminal Sessions (`terminal.go`)

`TerminalManager` keeps one PTY subprocess per chat session alive across WS disconnects.

- The output-pump goroutine deletes its session from `m.sessions` only if the map still holds the same `*ptySession` (owner-scoped delete, ADR 0003); never use an unconditional delete.
- `Attach` runs a watcher goroutine that closes the WS when `s.done` fires (CLI exit while the user is idle), torn down via `watchDone`. Keep it, or the handler goroutine and connection leak.

## Subtask Merge-Back Coordinator (ADR 0016)

`SubtaskCoordinator` is wired in `cmd/server/main.go`: `engine.OnTerminal` → `OnChildTerminal` (else the normal push/teardown), `pool.Subtasks` → `AfterParentRun`, `dispatcher.Subtasks` → `BuildConflictContext`. Child terminal → `--no-ff` merge into the parent branch; conflict → abort and `merge_status=merge_conflict`. Auto-advance requires every non-archived child terminal **and** `merged`, using `workflow.TriggerSubtasksComplete`. Per-parent mutex (`plocks`) then repo lock; merges are `pending` while the parent has a run in flight and flushed by `AfterParentRun`. Tests: `subtasks_e2e_test.go`, `subtasks_coord_test.go`.

## Environment Variable Security

Subprocess env is allow-listed, not inherited; see `providers/AGENTS.md` and ADR 0019. `mergeEnv` blocks hijack-prone keys (`PATH`, `LD_PRELOAD`, `HOME`, ...) with a logged warning.

## Adding a New Provider

1. Implement `Provider` in a new file in `providers/` (see `providers/AGENTS.md`).
2. Add a case to `providerFactory` in `cmd/server/main.go`.
3. Add the string to `knownProviders` in `internal/api/handlers/agents.go` (validated on agent-config and provider-config create/update).
4. Declare its capabilities in `frontend/src/lib/providerCapabilities.ts` and regenerate docs (root `AGENTS.md`).

Providers only read the flat `AgentConfig.Provider/.Model/.Env`; `toAgentConfig` (`dispatcher.go`) fills them from the joined `ProviderConfig` and chat resolves it in `handlers/chat.go` (ADR 0018, `docs/agents.md#provider-configs`).

## Logging Conventions

stdlib `log/slog` only. Every line carries `component` (`"dispatcher"` or `"pool"`) plus `task_id`/`run_id` when known. Build a scoped logger once (`log := slog.With("component", "dispatcher", "task_id", t.ID)`, rebind with `log.With("run_id", runID)`) at the top of `dispatch()`, `run()`, `resolveOutcome()`, `persistLogs()`; sweep-level logs use package-level `slog.Xxx` with an explicit `component`. Keep the `"dispatcher: ..."` / `"pool: ..."` message prefixes (codebase-wide convention, see also `ghsync`).
