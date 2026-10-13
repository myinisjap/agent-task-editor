# 29. The workflow engine is the only writer of label transitions

Status: Accepted

## Context

Labels move from many places: board drag-and-drop, Approve/Reject, agent
outcomes, subtask auto-advance, PR-review auto-transition. Gate rules and the
lock release must be identical for all of them.

## Decision

`workflow.Engine.Transition` validates in order: task exists; a
`workflow_transitions` row exists; `trigger_type = human` rejects an agent
trigger (`ErrGateRequired`); an `agent_ignore` destination rejects an agent
trigger (`ErrAgentIgnored`). It then writes the label via compare-and-swap
(`ErrStale` if the task moved concurrently, mapped to HTTP 409) plus a
`task_label_history` row in one transaction, clearing the lock, and publishes
`task.label_changed`. The pool uses `TransitionExpecting` with the dispatch-time
label so an agent outcome never lands on top of a human move made after the run
finished. Triggers: `agent`, `human`, and `TriggerSubtasksComplete` (ADR 0016).
`ghsync`'s per-repo opt-in PR-review auto-transition uses `TriggerHuman` to
mirror the manual Reject action and keeps its own `failurePathTarget` helper
rather than depending on `api/handlers`. Landing a new task on a column is
initial placement (`resolveInitialLabel`), not a transition.

## Consequences

- Do not update `tasks.label` directly; go through the engine.
- The engine accepts a `Publisher`; `*ws.Hub` satisfies both
  `workflow.Publisher` and `agent.Publisher`.
