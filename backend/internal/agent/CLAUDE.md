# internal/agent

The agent package owns everything to do with running AI agents: the provider abstraction, three concrete providers, the worker pool, and the dispatcher.

## Files

| File | Purpose |
|---|---|
| `provider.go` | `Provider` interface, `RunInput`, `Result`, `LogEntry`, `LogEntryType` |
| `claude.go` | `ClaudeRunner` — runs `claude` CLI subprocess with stream-json output |
| `anthropic.go` | `AnthropicRunner` — calls Anthropic Messages API directly |
| `llm.go` | `LLMRunner` — calls any OpenAI-compatible API |
| `tools.go` | Shared tool implementations for `anthropic` and `llm` providers (read_file, write_file, run_bash) |
| `mcp.go` | `MCPManager` — prepares/cleans up the MCP sidecar config and result file |
| `pool.go` | `Pool` — bounded goroutine pool; persists logs, publishes WS events |
| `dispatcher.go` | `Dispatcher` — periodic DB sweep; matches tasks to configs; submits jobs |
| `worktree.go` | Per-task git worktree provisioning, safety-net commit, diff, push, teardown |

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
- `failed` — something went wrong; task stays on current label (re-dispatch will retry)
- `waiting_human` — agent called `request_human`; `Message` surfaces to the UI

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
