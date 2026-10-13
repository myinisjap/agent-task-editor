# 26. Shutdown and crashes produce `interrupted` runs, not failures

Status: Accepted

## Context

Restarting the server used to mark in-flight runs `failed`, burning retry and
failure counters and escalating tasks that had done nothing wrong.

## Decision

On SIGTERM/SIGINT the dispatcher stops, in-flight runs continue for
`SHUTDOWN_DRAIN_TIMEOUT` (default 60s; 0 = no drain; HTTP, MCP sidecars and the
side-effect worker stay up), then `Pool.Shutdown(drain)` interrupts queued jobs,
waits, and cancels stragglers with `CancelReasonShutdown`. They are recorded
with the neutral `interrupted` status: no retry or failure counters or metrics,
task not paused, lock released, session id persisted so re-dispatch can resume
it. The server waits for the pool before closing the database. Runs left
`running`/`pending` by a crash are marked `interrupted` on boot by
`storage.RecoverInterruptedRuns` (`pending` covers a run created but never
enqueued), and boot also clears all `active_agent_run_id` values because the
pool restarted. `interrupted` is excluded from usage and dashboard aggregates.

## Consequences

- Container `stop_grace_period` must exceed the drain timeout.
- Do not treat `interrupted` as a failure anywhere.
