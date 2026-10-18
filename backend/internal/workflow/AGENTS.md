# internal/workflow

The state machine engine that validates and executes label transitions for tasks.

## Engine

```go
engine := workflow.New(db *sql.DB, pub Publisher)
engine.Transition(ctx, taskID, toLabel, trigger, actorID, note) error
engine.TransitionExpecting(ctx, taskID, toLabel, trigger, actorID, note, expectedFrom) error // ErrStale if task is not on expectedFrom
engine.AvailableTransitions(ctx, taskID, trigger) ([]string, error)
engine.AgentPickupLabels(ctx, workflowID) ([]string, error)
```

## Transition Validation

`Transition` checks in order:
1. Task exists
2. A `workflow_transitions` row exists for `(workflow_id, from_label, to_label)`
3. If `trigger_type = human` and trigger is `agent` → `ErrGateRequired`
4. If destination label has `agent_ignore = true` and trigger is `agent` → `ErrAgentIgnored`

On success, wraps two DB writes in a transaction:
- the label update — a **compare-and-swap** (`UPDATE tasks SET label = ? … WHERE
  id = ? AND label = ?`, the expected from-label) run as raw SQL on the tx, not
  the generated `UpdateTaskLabel` (sqlc's SQLite analyzer miscompiles the extra
  guard param — ADR 0020, `internal/storage/AGENTS.md`). It
  still clears `active_agent_run_id`. If it matches 0 rows, a concurrent
  transition already moved the task, so it returns `ErrStale` (no clobber).
- `CreateTaskLabelHistory` (audit log)

Then publishes `task.label_changed` to the WebSocket hub.

## Sentinel Errors

```go
ErrNoTransition   // (from→to) not in workflow_transitions
ErrGateRequired   // agent tried a human-only transition
ErrAgentIgnored   // destination has agent_ignore = true
ErrTaskNotFound   // task ID doesn't exist
ErrStale          // task's label changed concurrently (CAS lost); map to HTTP 409
```

## Trigger Types

```go
TriggerAgent TransitionTrigger = "agent"
TriggerHuman TransitionTrigger = "human"
```

Human transitions come from Approve/Reject/MoveLabel. Agent transitions come from the pool after a run completes with `NextLabel`; the pool uses `TransitionExpecting` with the dispatch-time label so an outcome never lands on a human move made after the run finished. `TriggerSubtasksComplete` is used by `SubtaskCoordinator` (ADR 0016) and bypasses the human/agent gate checks. `internal/ghsync`'s opt-in PR-review auto-transition uses `TriggerHuman` to mirror Reject, with its own `ghsync.(*Syncer).failurePathTarget` rather than depending on `api/handlers`. `ghsync.(*Syncer).finishMergedTask` uses `ForceTransition` to move a task whose PR merged onto the first terminal label, skipping tasks whose active run is pending/running. Do not write `tasks.label` outside the engine (ADR 0029).

## AgentPickupLabels

Used by `ListAgentPickupTasks` SQL query logic — returns label names where an agent-initiated transition exists from that label and the label itself is not `agent_ignore`. The dispatcher uses this indirectly via the SQL query.

## Publisher Interface

`workflow.Publisher` is satisfied by `*ws.Hub`. The engine calls `Publish("task.label_changed", ...)` after each successful transition.
