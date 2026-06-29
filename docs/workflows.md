# Workflows

A workflow is the state machine that governs how tasks progress. Each workflow contains **labels** (columns) and **transitions** (allowed moves between labels).

## Labels

| Field | Type | Description |
|---|---|---|
| `name` | string | Unique identifier within the workflow (used as the task's current state) |
| `color` | string | Hex color for UI display |
| `sort_order` | int | Column order on the board |
| `agent_ignore` | bool | Agents cannot move tasks to this label; dispatcher skips tasks already here |
| `is_terminal` | bool | No further transitions; task is complete |

## Transitions

Each transition defines an allowed `from → to` move:

| Field | Description |
|---|---|
| `from_label` | Source label name |
| `to_label` | Destination label name |
| `trigger_type` | `agent`, `human`, or `both` |
| `path` | `success` or `failure` — which outcome this transition represents (used by Approve/Reject) |

A task can only move to labels that have a matching transition from its current label. The workflow engine enforces this — any attempt to move outside the defined transitions returns `ErrNoTransition`.

## Default Workflow

Seeded automatically on first startup:

```
not_ready ──(human)──▶ plan ──(human)──▶ todo
                                           │
                                        (agent)
                                           ▼
                                      in-progress ◀──────┐
                                           │              │ (agent/human)
                                        (agent)           │
                                           ▼              │
                                        testing ──────────┤
                                           │              │
                                        (agent)           │
                                           ▼              │
                                      agent-review ────────┤
                                           │              │
                                        (agent)           │
                                           ▼
                                        review ──(human)──▶ done
                                           │
                                        (human)
                                           ▼
                                      in-progress
```

All labels can also be moved to `not_ready` by a human (parking).

**Label flags in the default workflow:**
- `not_ready` — `agent_ignore = true` (nothing runs here)
- `done` — `is_terminal = true`

## Approve and Reject

The `/tasks/{id}/approve` and `/tasks/{id}/reject` endpoints move tasks along human-gated transitions.

**Approve** (`POST /tasks/{id}/approve`) — follows the `success` human transition defined for the current label. Optional body:

```json
{ "note": "optional note recorded in label history" }
```

**Reject** (`POST /tasks/{id}/reject`) — follows the `failure` human transition defined for the current label. The `to_label` field overrides the auto-resolved target. The `note` is stored as feedback on the prior agent run and injected at the top of the next run's prompt under `"FEEDBACK FROM PRIOR REVIEW:"`.

```json
{
  "note": "Please fix the edge cases in the payment flow",
  "to_label": "optional override label"
}
```

If no matching transition is defined for the current label, the endpoint returns `400`.

## Custom Workflows

Create additional workflows via Settings → Workflows in the UI, or via the REST API:

```http
POST /api/v1/workflows
{ "name": "My Workflow", "description": "..." }
```

### Import / Export

Workflows can be exported as YAML for version control and imported into another instance:

```bash
# Export
GET /api/v1/workflows/{id}/export.yaml

# Import
POST /api/v1/workflows/import
Content-Type: application/yaml
<yaml body>
```

YAML format:

```yaml
name: My Workflow
description: Optional description
labels:
  - name: backlog
    color: "#6B7280"
    sort_order: 0
    agent_ignore: true
  - name: in-progress
    color: "#F59E0B"
    sort_order: 1
  - name: done
    color: "#10B981"
    sort_order: 2
    is_terminal: true
transitions:
  - from: backlog
    to: in-progress
    trigger: human
  - from: in-progress
    to: done
    trigger: agent
  - from: done
    to: in-progress
    trigger: human
```

## Workflow Engine Rules

1. A transition must exist in `workflow_transitions` for `(workflow_id, from_label, to_label)`.
2. If the transition has `trigger_type = human`, only human-triggered calls succeed; agent calls receive `ErrGateRequired`.
3. If the destination label has `agent_ignore = true`, agents cannot move tasks there (`ErrAgentIgnored`).
4. Every successful transition is recorded in `task_label_history` with the trigger, actor ID, and optional note.
5. Whenever `UpdateTaskLabel` runs (any transition), `active_agent_run_id` is automatically cleared — preventing stale dispatch locks.
