# REST API Reference

Base path: `/api/v1`

Authentication: `Authorization: Bearer <API_TOKEN>` on all requests (if `API_TOKEN` is set).

Request bodies are JSON unless noted. Responses are JSON. Request bodies are limited to 1 MB (task creation allows 50 MB for image uploads).

---

## Tasks

### Task Object

Key fields returned by task endpoints:

| Field | Type | Description |
|---|---|---|
| `id` | UUID | Task identifier |
| `title` | string | Task title |
| `description` | string | Task description |
| `type` | string | Task type (`feature`, `bug`, `chore`, etc.) |
| `label` | string | Current workflow label |
| `repo_id` | UUID | Associated repository |
| `workflow_id` | UUID | Associated workflow |
| `branch` | string | Git branch name (`ate-<slug>-<id>`); empty if not yet provisioned |
| `worktree_path` | string | Absolute path to the per-task git worktree; empty if torn down |
| `base_ref` | string | The git ref the branch was forked from |
| `agent_notes` | string | Persistent markdown notes written by agents |
| `git_state` | string | `""`, `pushed`, `pr_open`, `pr_merged`, `pr_closed` |
| `attachments` | string[] | Relative paths to uploaded attachment files |
| `active_agent_run_id` | UUID? | Set while an agent run is in progress |
| `current_agent_run_id` | UUID? | ID of the most recent agent run |

---

### `GET /tasks`
List all tasks. Returns an array of task objects.

Query params: `label` (filter by label name).

### `POST /tasks`
Create a task. Accepts JSON body or `multipart/form-data` (for image attachments).

**JSON body:**
```json
{
  "title": "string (required)",
  "description": "string",
  "type": "feature | bug | chore | ...",
  "repo_id": "uuid (required)",
  "workflow_id": "uuid (required)"
}
```

**Multipart form** (`Content-Type: multipart/form-data`):
Same fields as form values, plus `attachments` (multiple file fields). Images are validated (max 10 MB each, image/* MIME type only) and stored in `UPLOAD_DIR`.

New tasks start on the `not_ready` label regardless of input.

### `GET /tasks/{id}`
Get a single task.

### `PATCH /tasks/{id}`
Update task fields (title, description, type, repo_id).

### `DELETE /tasks/{id}`
Delete a task and all associated runs/logs. Also tears down the per-task git worktree and removes uploaded attachments.

### `PATCH /tasks/{id}/label`
Move the task to a different label via the workflow engine. Goes through normal transition validation — `to_label` must be a valid transition from the current label.

```json
{ "to_label": "label-name", "note": "optional note" }
```

Returns `400` if no transition exists, `403` if the transition requires human auth or the target label is `agent_ignore`.

### `POST /tasks/{id}/approve`
Human approval — follows the `success` human transition from the task's current label.

```json
{ "note": "optional note recorded in label history" }
```

Body is optional. Returns `400` if no `success` human transition is defined for the current label.

### `POST /tasks/{id}/reject`
Human rejection — follows the `failure` human transition from the task's current label. The `note` is stored as feedback on the prior agent run and appears at the top of the next agent's prompt. `to_label` overrides the auto-resolved target.

```json
{
  "note": "optional feedback message",
  "to_label": "optional override label"
}
```

Returns `400` if no `failure` human transition is defined and `to_label` is not provided.

### `PATCH /tasks/{id}/notes`
Update the task's agent notes directly. Useful for UI or tooling that wants to set notes without an agent run.

```json
{ "notes": "markdown content", "append": false }
```

If `append` is `true`, new content is appended to existing notes with a blank line separator.

### `POST /tasks/{id}/rerun`
Clears `active_agent_run_id` to allow the dispatcher to re-dispatch the task. Use when a run got stuck.

Returns `204 No Content`.

### `GET /tasks/{id}/diff`
Get the task's accumulated changes — the diff of its per-task branch against the merge-base with the ref it forked from.

Returns `{ "branch": "...", "diff": "..." }`. `diff` is empty until an agent has been dispatched and a branch provisioned. When the task reaches a terminal label and the worktree is torn down, the diff is computed against the main repo clone (branch is preserved).

### `GET /tasks/{id}/pr-url`
Returns `{ "url": "..." }` — a GitHub `compare` URL with the PR **title and body pre-filled** (task title, description, agent notes, and commit subjects). Open it to create a fully-described PR in one click; no GitHub auth or `gh` CLI needed. Requires the repo to have a GitHub remote and the task to have a provisioned branch (else `400`).

### `GET /tasks/{id}/github-status`
Fetches live GitHub PR state for the task's branch. Returns:
```json
{ "git_state": "pr_open", "pr_url": "https://github.com/..." }
```

### `PATCH /tasks/{id}/git-state`
Manually set the git state. Valid values: `""`, `pushed`, `pr_open`, `pr_merged`, `pr_closed`.

```json
{ "git_state": "pr_merged" }
```

### `PATCH /tasks/{id}/pause`
Pause or unpause a task. A paused task is never picked up by the dispatcher
(enforced in `ListAgentPickupTasks`), regardless of its current label. Pausing
does not change the task's `label` and does not cancel an in-flight agent run —
it only blocks future dispatch. The flag persists across server restarts (it's
a DB column, not in-memory state).

```json
{ "paused": true }
```

Returns the updated `Task` object (with `paused: true`/`false`).

---

## Agent Runs

### AgentRun Object

| Field | Type | Description |
|---|---|---|
| `id` | UUID | Run identifier |
| `task_id` | UUID | Associated task |
| `agent_config_id` | UUID | Agent config used for this run |
| `status` | string | `pending`, `running`, `completed`, `failed`, `waiting_human` |
| `feedback` | string? | Feedback set on rejection (injected into the next run's prompt) |
| `stored_info` | string? | Info stored by the agent via `store_info`; visible in the task UI |
| `created_at` | RFC3339 | When the run was created |
| `started_at` | RFC3339? | When the run started executing |
| `completed_at` | RFC3339? | When the run finished |

### `GET /tasks/{id}/runs`
List all agent runs for a task (newest first).

### `GET /tasks/{id}/runs/{run_id}`
Get a single run record.

### `GET /tasks/{id}/runs/{run_id}/logs`
Get all persisted log entries for a run.

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

## Uploads

### `GET /uploads/{task_id}/{filename}`
Serve a task attachment image. Not auth-gated by default (images are referenced by opaque UUIDs). Used by the frontend to display attached images.

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

### `PUT /workflows/{id}/yaml`
Replace a workflow's definition from YAML. Body is `application/yaml` or `text/yaml`.

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
  "provider": "claude | anthropic | opencode | qwen_code | llm | ...",
  "model": "string",
  "labels": ["label1", "label2"],
  "system_prompt": "string",
  "max_tokens": 0,
  "timeout_secs": 600,
  "env": { "KEY": "value" }
}
```

If a label conflict exists with an already-enabled config, the new config is created in disabled state. The response includes an `X-Label-Conflict` header with the conflicting config name.

### `GET /agents/{id}`
Get a single agent config.

### `PUT /agents/{id}`
Replace an agent config. Supports `"enabled": true|false` to enable/disable. Enabling checks for label conflicts.

### `DELETE /agents/{id}`
Delete an agent config.

### `GET /agents/models?provider=<provider>`
Get the list of available models for a provider. Currently supports:
- `provider=claude` — returns a static list of Claude models
- `provider=opencode` — runs `opencode models` and returns its output

Returns:
```json
{
  "provider": "claude",
  "default_model": "claude-sonnet-4-6",
  "models": ["claude-sonnet-4-6", "claude-opus-4"]
}
```

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

## GitHub

### `GET /github/auth-status`
Returns whether GitHub CLI credentials are present (used by the frontend to show/hide GitHub-related UI). Not a hard auth check.

---

## Dashboard

### `GET /dashboard`
Returns aggregated statistics:

```json
{
  "total_tasks": 42,
  "tasks_by_label": { "plan": 5, "work": 3 },
  "total_runs": 120,
  "runs_by_status": { "completed": 100, "failed": 8, "running": 2 },
  "recent_runs": [ ... ]
}
```

---

## Health

### `GET /healthz`
Returns `200 OK` with `{"status":"ok"}`. Not auth-gated.
