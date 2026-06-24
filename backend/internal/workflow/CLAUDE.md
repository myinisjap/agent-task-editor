# internal/workflow

The state machine engine that validates and executes label transitions for tasks.

## Engine

```go
engine := workflow.New(db *sql.DB, pub Publisher)
engine.Transition(ctx, taskID, toLabel, trigger, actorID, note) error
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
- `UpdateTaskLabel` (updates the task's label; also clears `active_agent_run_id`)
- `CreateTaskLabelHistory` (audit log)

Then publishes `task.label_changed` to the WebSocket hub.

## Sentinel Errors

```go
ErrNoTransition   // (from→to) not in workflow_transitions
ErrGateRequired   // agent tried a human-only transition
ErrAgentIgnored   // destination has agent_ignore = true
ErrTaskNotFound   // task ID doesn't exist
```

## Trigger Types

```go
TriggerAgent TransitionTrigger = "agent"
TriggerHuman TransitionTrigger = "human"
```

Human-triggered transitions come from the Approve/Reject handlers. Agent-triggered transitions come from the pool after a run completes with `NextLabel` set.

## AgentPickupLabels

Used by `ListAgentPickupTasks` SQL query logic — returns label names where an agent-initiated transition exists from that label and the label itself is not `agent_ignore`. The dispatcher uses this indirectly via the SQL query.

## Publisher Interface

`workflow.Publisher` is satisfied by `*ws.Hub`. The engine calls `Publish("task.label_changed", ...)` after each successful transition.
