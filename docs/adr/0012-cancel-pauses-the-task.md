# 12. Cancelling a run pauses the task

Status: Accepted

## Context

`Pool.Cancel(runID)` kills an in-flight run. Clearing the dispatch lock alone
would let the next 5s sweep re-dispatch the very run just killed.

## Decision

The pool keeps a per-run cancel registry. `run()` registers a cancel func
before calling the provider, so CLI subprocesses (`exec.CommandContext`) are
killed and HTTP providers abort. When the provider returns, `run()` checks the
`cancelled` flag **before** error classification (a cancelled provider
surfaces a transient-looking error) and calls `handleCancelled`: mark the run
`cancelled` (not a failure), reset the transient retry budget, **pause the
task**, clear the lock, and publish `task.agent_done` plus `task.updated`.

`POST /tasks/{id}/runs/{run_id}/cancel` only signals: 202 once signalled, 409 if
the run is not running or no longer registered, 404 if it belongs to another
task. DB writes and broadcasts happen in the pool goroutine, behind the
`RunCanceller` interface so tests inject a fake.

## Consequences

- A human must resume the task. Pause never cancels a running run; it only
  stops new dispatch (`paused = 0` is filtered in SQL, with a second check in
  `dispatch()`).
- `cancelled` runs are excluded from usage and dashboard aggregates.
