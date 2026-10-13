# 2. `waiting_human` runs keep the dispatch lock

Status: Accepted

## Context

When an agent calls `request_human`, hits an auth error, exhausts its retry
budget, runs out of cost budget or turns, or loops on a failure transition, the
task needs a human. If the lock were cleared the next 5s dispatcher sweep would
immediately start another run and the human would never get a chance to act.

## Decision

A run that ends `waiting_human` does **not** clear `active_agent_run_id`. The
lock is released only by an explicit action: a label transition
(`UpdateTaskLabel` / `engine.Transition` always clear it), a reply
(`DispatchReply` moves the lock to the new run), or `Rerun`'s explicit human
force-clear. The same rule covers every escalation path (transient-retry
exhaustion, `max_retries = 0`, mid-run cost kill, max turns, rework-loop
escalation, no-outcome escalation).

## Consequences

- "Waiting on a human" is observable as "lock set and run is `waiting_human`";
  no separate flag is needed.
- Any new escalation path must keep the lock and publish `task.needs_human`.
- The replied-to run keeps its `waiting_human` status; only the lock moves.
- Cancelling a run is the opposite case and is handled by pausing (ADR 0012).
