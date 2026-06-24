# internal/api/handlers

One file per resource group. All handlers receive a `*gen.Queries` for database access plus resource-specific dependencies.

## Files

| File | Handler | Key Dependencies |
|---|---|---|
| `tasks.go` | `TasksHandler` | `gen.Queries`, `workflow.Engine` |
| `workflows.go` | `WorkflowsHandler` | `gen.Queries`, `*sql.DB` (for YAML import transactions) |
| `agents.go` | `AgentsHandler` | `gen.Queries` |
| `repos.go` | `ReposHandler` | `gen.Queries`, `repoBaseDir string` |
| `dashboard.go` | `DashboardHandler` | `gen.Queries` |
| `health.go` | `Health` func | none |
| `workflow_yaml.go` | helpers for `WorkflowsHandler` | — |

## Tasks Handler Notes

- **Approve** (`POST /tasks/{id}/approve`) — uses `engine.AvailableTransitions` with `TriggerHuman`, skips the `is_rejection_target` label when picking the default destination, calls `engine.Transition`.
- **Reject** (`POST /tasks/{id}/reject`) — looks up the workflow's `is_rejection_target` label via `GetWorkflowRejectionLabel`; falls back to `"in-progress"` if none defined; optional `feedback` field is stored on the current run.
- **MoveLabel** (`PATCH /tasks/{id}/label`) — direct move bypassing workflow transition validation; used by board drag-and-drop.

## Repos Handler Notes

When `repoBaseDir` is non-empty, the `Create` handler rejects paths outside that directory. It resolves symlinks via `filepath.EvalSymlinks` (falls back to `filepath.Clean` if the path doesn't exist yet) before comparing.

## Workflow YAML Handler Notes

`ExportWorkflowYAML` and `ImportWorkflowYAML` live in `workflow_yaml.go`. Import wraps all inserts in a single transaction to keep the workflow consistent on partial failure.

## Response Helpers

Defined in `helpers.go` (or inline):

```go
JSON(w, status, v)     // marshal v as JSON with status
Err(w, status, msg)    // { "error": msg }
```
