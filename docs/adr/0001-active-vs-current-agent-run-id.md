# 1. Separate `active_agent_run_id` from `current_agent_run_id`

Status: Accepted

## Context

A task needs to answer two different questions: "is a run in flight (or
awaiting a human) so the dispatcher must not start another?" and "which run's
logs should a WebSocket subscriber see?". One column cannot answer both,
because the dispatch gate must be cleared on completion while the replay
pointer must survive it (clients open finished tasks and expect to see the last
run's logs).

## Decision

Two columns on `tasks`:

- `active_agent_run_id` is the dispatch lock. The dispatcher sets it when it
  creates a run and the pool clears it on completion. A non-null value blocks
  re-dispatch.
- `current_agent_run_id` is the last run ever associated with the task. It is
  never cleared. WS replay (`ws/client.go`) and the dispatcher's
  prior-feedback lookup (`startRun`) key off it.

`SetTaskActiveRun` sets both. `SetTaskActiveRunOnly` sets only the lock and is
used by synthetic escalation runs (cost-budget phantom `waiting_human` runs)
that have no logs or feedback and must not become the replayed run or the
source of rework feedback.

## Consequences

- Code that needs a lock must use `active_agent_run_id`; code that needs "the
  last run" must use `current_agent_run_id`. Mixing them up either blocks
  dispatch forever or replays the wrong logs.
- Synthetic runs need the `...ActiveRunOnly` variant, or must restore
  `CurrentAgentRunID` to its pre-dispatch value (the nil-provider path does
  the latter because `persistRunRow` has already set both columns).
- See also ADR 0002 and ADR 0003 for lock semantics.
