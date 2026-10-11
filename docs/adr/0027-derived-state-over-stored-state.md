# 27. Dispatch gates are independent columns or derived, never inferred from label

Status: Accepted

## Context

Several conditions stop a task from being dispatched: paused, archived,
blocked by dependencies, in retry backoff, over budget, human-gated label.
Coupling them to the label or to each other makes them hard to reason about and
requires write-time bookkeeping.

## Decision

- `paused` and `archived` are persisted boolean columns, independent of `label`
  and of the lock, and survive restarts. `ListAgentPickupTasks` filters them in
  SQL (`paused = 0`, not archived); `dispatch()` re-checks `Paused` as defense
  in depth. Pausing never cancels a running run.
- "Blocked" is **derived**, never a column: `ListAgentPickupTasks` has a
  `NOT EXISTS (unsatisfied blocker)` clause, where a blocker is a dependency
  that is neither archived nor on a terminal label. Nothing needs to fire when
  a blocker finishes; the next sweep sees the task differently. Dependency
  edges are added in a transaction with cycle detection (Go DFS over
  `ListWorkflowDependencyEdges`).
- Priority (`-1..2`) only orders pickup (`priority DESC, created_at ASC`); it
  never preempts a run or bypasses another gate. `queue_position` is derived by
  ranking a fresh `ListAgentPickupTasks` call.
- Schedules (`task_schedules`) dedupe with `HasOpenTaskForSchedule` (same
  "open" idiom) and a per-firing `source_ref` of `<schedule_id>#<marker>`
  because `(source, source_ref)` is unique.
- Timestamps-only background updates such as `SetTaskPRMergeable` leave
  `updated_at` alone so a poll does not make a task look freshly edited.

## Consequences

- Add new gates as SQL filters in `ListAgentPickupTasks` (and the matching
  Go-side re-check), not as label hacks.
