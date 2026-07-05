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
not_ready ──(human)──▶ plan ──(agent, success)──▶ review-plan
                                                        │
                                    ┌───────────────────┴───────────────────┐
                                    │ (human, failure)                      │ (human, success)
                                    ▼                                       ▼
                                  plan                                    work
                                                                            │
                                                                       (agent, success)
                                                                            ▼
                                                                         testing
                                                                            │
                                              ┌─────────────────────────────┤
                                              │ (agent, failure)            │ (agent, success)
                                              ▼                             ▼
                                             work                    agent-review
                                                                            │
                                              ┌─────────────────────────────┤
                                              │ (agent, failure)            │ (agent, success)
                                              ▼                             ▼
                                             work                        review
                                                                            │
                                              ┌─────────────────────────────┤
                                              │ (human, failure)            │ (human, success)
                                              ▼                             ▼
                                             work                         done
```

`not_ready` has no incoming transitions in the default workflow — tasks start there and move forward via `not_ready → plan`, but nothing routes back to it automatically.

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

## Task Dependencies

A task can declare **dependencies** on other tasks in the same workflow — a way
to express "don't dispatch B until A is done" so multi-task work can be queued
without babysitting the board.

- **Dispatch-only gate.** A task with at least one *unsatisfied* blocker is never
  picked up by the dispatcher. It is not paused, archived, or moved — it simply
  sits on its current label until its blockers finish. Humans can still drag it
  anywhere; dragging a blocked task into an agent-triggerable column pops a
  confirmation, and the task stays un-dispatched (and visibly muted with a
  "blocked by N" badge) until the block clears.
- **Satisfied = terminal or archived.** A blocker satisfies its edge once it
  reaches a label with `is_terminal = true`, **or** is archived. Archiving is the
  existing "this is over" gesture; treating an archived blocker as forever-blocking
  would create an invisible deadlock. (Un-archiving makes the edge unmet again.)
- **Derived, never stored.** "Blocked" is computed at read time from the blocker's
  current state — there is no status column to drift and no event required when a
  blocker completes. The next dispatch sweep just sees the task as eligible.
- **No cycles, no self-edges, no cross-workflow edges.** These are rejected when
  the edge is created. A blocker whose workflow has no terminal label is also
  rejected, since such an edge could never satisfy.
- **Not parent/child.** A dependency is a peer relationship with no automation: a
  dependent task always has its own work, and simply becomes dispatch-eligible on
  its current label once its blockers finish. (Parent/child subtask decomposition
  is a separate, future mechanism.)

Manage dependencies from a task's detail page (the Dependencies section) or via
`GET/POST /tasks/{id}/dependencies` and `DELETE /tasks/{id}/dependencies/{dep_id}`.
Task list/detail responses carry derived `blocked_by_count` and `blocking_count`.

## Subtasks (agent-driven decomposition)

A planning agent can split a large task into structured child tasks instead of
leaving prose in `agent_notes`. Subtasks build on the dependency gate above.

- **Opt-in per agent config.** Only a config with `subtasks_enabled` (off by
  default) exposes the `create_subtask` MCP tool; `max_subtasks` (default 10)
  caps children per parent. Typically only the planner gets this. The
  `create_subtask` tool writes live to `POST /tasks/{id}/subtasks`, so children
  appear on the board mid-run.
- **Human gate.** Children land on the workflow's first `agent_ignore` label
  (the seed workflow: `not_ready`) so a human sanity-checks the decomposition
  before agents fan out, then releases them (bulk-move out of the gate).
- **Relationship.** `parent_task_id` groups a child under its parent (rollup,
  provenance via `created_by_run_id`); an auto-created **parent→child dependency
  edge** is the dispatch gate, so the parent isn't dispatched until every child
  reaches a terminal label. Depth is limited to 1 — a subtask can't create
  subtasks.
- **Branch off parent, merge back.** A child's worktree is cut from the parent's
  branch. When a child reaches a terminal label its branch is merged back into
  the parent's branch (a plain merge commit); the child's worktree/branch are
  then removed. Children never push to origin or open PRs — the parent's branch
  (and its single eventual PR) is the only outward-facing artifact, and
  `GET /tasks/{parent}/diff` shows the integrated result.
- **Conflicts.** A conflicting merge-back is aborted cleanly and the child is
  flagged `merge_conflict`. Because the child is terminal, the parent's edge is
  satisfied and the parent becomes dispatch-eligible; the dispatcher hands the
  parent's `work` agent the conflict context to resolve the merge on the parent
  branch. A human can also resolve it manually.
- **Auto-advance.** Once every child is terminal and merged cleanly, the parent
  advances along its agent-success transition (`work → testing` in the seed
  workflow), recorded in `task_label_history` with the `subtasks_complete`
  trigger. If the parent is paused, has a run in flight, or has no agent-success
  transition, the auto-advance is skipped and the parent is simply left
  unblocked for a human or the next dispatch to drive.

Manage subtasks from a task's detail page (the Subtasks section) or via
`POST /tasks/{id}/subtasks` and `GET /tasks?parent_id=`. Task responses carry
`parent_task_id`, `merge_status`, and derived `subtask_total`/`subtask_done`/
`subtask_conflicts`.
