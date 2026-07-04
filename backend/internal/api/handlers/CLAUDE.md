# internal/api/handlers

One file per resource group. All handlers receive a `*gen.Queries` for database access plus resource-specific dependencies.

## Files

| File | Handler | Key Dependencies |
|---|---|---|
| `tasks.go` | `TasksHandler` | `gen.Queries`, `workflow.Engine` |
| `workflows.go` | `WorkflowsHandler` | `gen.Queries`, `*sql.DB` (for YAML import transactions) |
| `agents.go` | `AgentsHandler` | `gen.Queries` |
| `repos.go` | `ReposHandler` | `gen.Queries`, `repoBaseDir string` |
| `review_comments.go` | `ReviewCommentsHandler` | `gen.Queries` |
| `dashboard.go` | `DashboardHandler` | `gen.Queries` |
| `health.go` | `Health` func | none |
| `workflow_yaml.go` | helpers for `WorkflowsHandler` | — |

## Tasks Handler Notes

- **Approve** (`POST /tasks/{id}/approve`) — follows the `success` human transition from the task's current label (via `humanPathTarget`), then calls `engine.Transition`. Returns `400` if no such transition exists.
- **Reject** (`POST /tasks/{id}/reject`) — follows the `failure` human transition from the current label; the optional `to_label` body field overrides it. Returns `400` if no `failure` transition exists and no override is given.
- **MoveLabel** (`PATCH /tasks/{id}/label`) — human-triggered move validated through `engine.Transition`; used by board drag-and-drop.

## Review Comments Handler Notes

Persistent inline diff review comments (`task_review_comments`). Open comments are injected into every agent run's prompt by the dispatcher; agents resolve them via the MCP sidecar's `resolve_comment` tool (applied by the pool on successful completion), and humans resolve/reopen via `PATCH /tasks/{id}/review-comments/{comment_id}`. Resolving an already-resolved comment returns `404` (the SQL guard is `status = 'open'`).

## Repos Handler Notes

When `repoBaseDir` is non-empty, the `Create` handler rejects paths outside that directory. It resolves symlinks via `filepath.EvalSymlinks` (falls back to `filepath.Clean` if the path doesn't exist yet) before comparing.

Issue sync (`issue_sync_enabled` / `issue_sync_label`): enabling requires both a `remote_url` and a `workflow_id` (Create returns `400` otherwise; Update validates the merged result). `PATCH` merges — omitted fields keep their existing values.

## Workflow YAML Handler Notes

`ExportWorkflowYAML` and `ImportWorkflowYAML` live in `workflow_yaml.go`. Import wraps all inserts in a single transaction to keep the workflow consistent on partial failure.

## Response Helpers

Defined in `helpers.go` (or inline):

```go
JSON(w, status, v)     // marshal v as JSON with status
Err(w, status, msg)    // { "error": msg }
```
