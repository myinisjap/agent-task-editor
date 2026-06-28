# REST API Reference

Base path: `/api/v1`

Authentication: `Authorization: Bearer <API_TOKEN>` on all requests (if `API_TOKEN` is set).

Request bodies are JSON. Responses are JSON. Request bodies are limited to 1 MB.

---

## Tasks

### `GET /tasks`
List all tasks. Returns an array of task objects.

Query params: `workflow_id`, `repo_id`, `label` (all optional filters).

### `POST /tasks`
Create a task.

```json
{
  "title": "string (required)",
  "description": "string",
  "type": "feature | bug | chore | ...",
  "label": "string (defaults to first workflow label)",
  "repo_id": "uuid (required)",
  "workflow_id": "uuid (required)"
}
```

### `GET /tasks/{id}`
Get a single task.

### `PATCH /tasks/{id}`
Update task fields (title, description, type).

### `DELETE /tasks/{id}`
Delete a task and all associated runs/logs.

### `PATCH /tasks/{id}/label`
Move the task to a different label directly (bypasses workflow validation — for board drag-and-drop). The `to` field must be a valid label name in the task's workflow.

```json
{ "to": "label-name" }
```

### `POST /tasks/{id}/approve`
Human approval — follows the `success` human transition from the task's current label.

```json
{ "to_label": "optional override" }
```

### `POST /tasks/{id}/reject`
Human rejection — follows the `failure` human transition from the task's current label (override with `to_label`).

```json
{ "to_label": "optional override", "feedback": "optional message for agent" }
```

### `GET /tasks/{id}/diff`
Get the task's accumulated changes — the diff of its per-task branch against the
merge-base with the ref it forked from. Each task works in its own git worktree
on its own branch (`ate-<slug>-<id>`), so concurrent agents on the same repo
don't conflict. Returns `{ "branch": "...", "diff": "..." }`; `diff` is empty
until the task has been dispatched and a branch provisioned.

### `GET /tasks/{id}/pr-url`
Returns `{ "url": "..." }` — a GitHub `compare` URL for the task's branch with the
PR **title and body pre-filled** (task description, agent notes, and commit
subjects). Open it to create a fully-described PR in one click; no GitHub auth or
`gh` CLI needed. Requires the repo to have a GitHub remote and the task to have a
provisioned branch (else `400`).

---

## Agent Runs

### `GET /tasks/{id}/runs`
List all agent runs for a task (newest first).

### `GET /tasks/{id}/runs/{run_id}`
Get a single run record including status, timestamps, and feedback.

### `GET /tasks/{id}/runs/{run_id}/logs`
Get all persisted log entries for a run.

Response:
```json
[
  {
    "id": "uuid",
    "agent_run_id": "uuid",
    "type": "system | stdout | stderr | tool_call | tool_result",
    "content": "string",
    "timestamp": "RFC3339"
  }
]
```

---

## Workflows

### `GET /workflows`
List all workflows.

### `POST /workflows`
Create a workflow.

```json
{ "name": "string", "description": "string" }
```

### `GET /workflows/{id}`
Get a workflow with its labels and transitions.

### `PUT /workflows/{id}`
Replace a workflow's full definition (labels + transitions).

### `DELETE /workflows/{id}`
Delete a workflow (fails if any tasks reference it).

### `GET /workflows/{id}/export.yaml`
Download the workflow as a YAML file.

### `POST /workflows/import`
Import a workflow from YAML. Body is `application/yaml` or `text/yaml`.

---

## Agent Configs

### `GET /agents`
List all agent configs.

### `POST /agents`
Create an agent config.

```json
{
  "name": "string",
  "provider": "claude | anthropic | llm",
  "model": "string",
  "labels": ["label1", "label2"],
  "system_prompt": "string",
  "max_tokens": 0,
  "timeout_secs": 600,
  "env": { "KEY": "value" }
}
```

### `GET /agents/{id}`
Get a single agent config.

### `PUT /agents/{id}`
Replace an agent config.

### `DELETE /agents/{id}`
Delete an agent config.

---

## Repositories

### `GET /repos`
List registered repositories.

### `POST /repos`
Register a repository.

```json
{ "name": "string", "path": "/absolute/path/to/repo" }
```

If `REPO_BASE_DIR` is set, `path` must be within that directory.

### `GET /repos/{id}`
Get a repository record.

### `DELETE /repos/{id}`
Unregister a repository.

### `GET /repos/{id}/tree`
List files in the repository (recursive, respects `.gitignore`).

---

## Dashboard

### `GET /dashboard`
Returns aggregated statistics:

```json
{
  "total_tasks": 42,
  "tasks_by_label": { "todo": 5, "in-progress": 3, ... },
  "total_runs": 120,
  "runs_by_status": { "completed": 100, "failed": 8, "running": 2 },
  "recent_runs": [ ... ]
}
```

---

## Health

### `GET /healthz`
Returns `200 OK` with `{"status":"ok"}`. Not auth-gated.
