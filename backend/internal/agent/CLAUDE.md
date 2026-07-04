# internal/agent

The agent package owns everything to do with running AI agents: the provider abstraction, three concrete providers, the worker pool, and the dispatcher.

## Files

| File | Purpose |
|---|---|
| `provider.go` | `Provider` interface, `RunInput`, `Result`, `LogEntry`, `LogEntryType` |
| `claude.go` | `ClaudeRunner` — runs `claude` CLI subprocess with stream-json output |
| `claude_discovery.go` | Discovers Claude plugins (`~/.claude/plugins/installed_plugins.json`) and user-level MCP servers (`~/.claude.json`'s global `mcpServers`) installed/configured on the machine, for per-agent-config selection (`enabled_plugins`/`enabled_mcp_servers`); `claude`-provider only |
| `anthropic.go` | `AnthropicRunner` — calls Anthropic Messages API directly |
| `llm.go` | `LLMRunner` — calls any OpenAI-compatible API |
| `tools.go` | Shared tool implementations for `anthropic` and `llm` providers (read_file, write_file, run_bash) |
| `mcp.go` | `MCPManager` — prepares/cleans up the MCP sidecar config and result file |
| `pool.go` | `Pool` — bounded goroutine pool; persists logs, publishes WS events; classifies transient vs genuine failures and drives the per-task retry budget |
| `dispatcher.go` | `Dispatcher` — periodic DB sweep; matches tasks to configs; submits jobs |
| `worktree.go` | Per-task git worktree provisioning, safety-net commit, diff, push, teardown |
| `errors.go` | `ErrTransient` — marks an error as a transient infra problem rather than a genuine task failure |
| `ratelimit.go` | `ErrRateLimit`, `RateLimitRegistry` (per-config 429 blocking), `BackoffDuration(WithBase)` exponential-backoff helpers |

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

## Retry Policy (Transient vs Genuine Failures)

Per-`AgentConfig` fields `max_retries` (default 3, 0 disables auto-retry) and
`retry_backoff_secs` (default 30, base for exponential backoff capped at 10m)
govern automatic retries for **transient** provider errors only:

- **Classification** (`errors.go`, `ratelimit.go`): any error implementing
  `Transient() bool` (both `ErrRateLimit` and `ErrTransient`) is treated as
  transient. HTTP providers (`anthropic.go`, `llm.go`) wrap network-level
  `Do()` errors and `5xx` responses as `ErrTransient`; `429` stays
  `ErrRateLimit`. CLI providers (`claude.go`, `qwen.go`, `opencode.go`)
  best-effort sniff stdout/stderr text (`isTransientLine`) for signals like
  connection resets, `502/503/504`, or "timeout", and also treat an ambiguous
  run-timeout (context deadline exceeded) as transient. A plain non-zero CLI
  exit with no such signal, or a `Result{Status:"failed"}` with no error at
  all, is a **genuine** failure and does not consume retry budget.
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
  different scopes (config-wide throttle vs per-task retry cap).

## Review Comment Feedback Loop

Humans leave persistent, file/line-anchored review comments on a task's diff
(`task_review_comments`, managed via `/tasks/{id}/review-comments`). The
dispatcher loads the task's **open** comments into
`RunInput.OpenReviewComments`; `buildPrompt` renders them (with `comment_id`s)
under `"OPEN REVIEW COMMENTS"`, so every provider sees them on every run until
resolved. CLI providers with the MCP sidecar (`claude`, `qwen_code`) expose a
`resolve_comment(comment_id, note)` tool; the sidecar accumulates resolutions
in the result file and the pool applies them to the DB **only when the run
completes successfully** (a failed run's claimed fixes never reached the
branch), then publishes `task.review_comments_changed`. Humans can also
resolve/reopen comments directly in the UI.

## Dispatch / Active Run Locking

`active_agent_run_id` prevents double-dispatch:
- Dispatcher sets it when creating a run
- Pool clears it on `completed` / `failed`
- Pool leaves it set on `waiting_human`
- `UpdateTaskLabel` (any workflow transition) always clears it via SQL

A task's `paused` flag (a persisted DB column, set via `PATCH
/tasks/{id}/pause`) is filtered out at the SQL level in
`ListAgentPickupTasks` (`AND t.paused = 0`), regardless of label or
`active_agent_run_id`. `dispatch()` also re-checks `t.Paused` as
defense-in-depth. Pausing does not cancel an already-running agent run; it
only prevents the dispatcher from starting a new one.

## Environment Variable Security

`mergeEnv` (in `claude.go`) blocks keys that could hijack the subprocess: `PATH`, `LD_PRELOAD`, `LD_LIBRARY_PATH`, `HOME`, `SHELL`, `IFS`, `DYLD_INSERT_LIBRARIES`, `DYLD_LIBRARY_PATH`. Blocked keys are logged as warnings, not silently dropped.

## Adding a New Provider

1. Implement `Provider` in a new file (e.g. `gemini.go`)
2. Add a new case to `providerFactory` in `cmd/server/main.go`
3. Add the provider string to the `AgentConfig.Provider` validation if any

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
