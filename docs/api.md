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
| `pr_url` | string | URL of the GitHub PR for the branch (set by `POST /tasks/{id}/pr` or the ghsync sweep); empty until a PR exists |
| `attachments` | string[] | Relative paths to uploaded attachment files |
| `paused` | boolean | Paused tasks are never picked up by the dispatcher |
| `archived` | boolean | Archived tasks are hidden from the default board view, skipped by the GitHub PR sweep, and never dispatched |
| `active_agent_run_id` | UUID? | Set while an agent run is in progress |
| `current_agent_run_id` | UUID? | ID of the most recent agent run |

---

### `GET /tasks`
List tasks, newest first. Returns an array of task objects (the body shape is
unchanged; pagination is carried in a response header). Archived tasks are
excluded unless `archived` is passed.

Query params (all optional, combinable):

| Param | Meaning |
|---|---|
| `q` | Case-insensitive substring search over title and description |
| `label` | Filter by label name |
| `repo_id` | Filter by repository |
| `type` | Filter by task type (`feature`, `bug`, …) |
| `git_state` | Filter by git state (`pushed`, `pr_open`, …) |
| `archived` | `all` includes archived tasks, `only` returns just archived tasks; omitted = hide archived |
| `limit` | Page size (default 200, clamped to 500) |
| `after` | Cursor for the next page — the id of the last task from the previous page |

**Pagination:** results are cursor-paginated on `(created_at, id)`. When more
tasks remain, the response includes an `X-Next-Cursor` header whose value is the
id to pass as `after` on the next request. The header is absent on the final
page. To load everything, page until `X-Next-Cursor` is no longer present.

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

### `GET /tasks/{id}/review-comments`
List all inline diff review comments on the task (open and resolved), ordered oldest-first.

### `POST /tasks/{id}/review-comments`
Add a persistent, file/line-anchored review comment to the task's diff:

```json
{
  "file_path": "src/main.go",
  "side": "new",
  "start_line": 10,
  "end_line": 12,
  "quoted_text": "x := 1",
  "body": "use the existing helper here"
}
```

While a comment is **open**, it is injected into every subsequent agent run's prompt under `"OPEN REVIEW COMMENTS"` with its `comment_id`. Agents address the comment and resolve it via the MCP sidecar's `resolve_comment` tool; the server applies resolutions only when the run completes successfully. Humans can resolve/reopen via `PATCH`.

### `PATCH /tasks/{id}/review-comments/{comment_id}`
Resolve or reopen a comment:

```json
{ "status": "resolved", "resolution_note": "renamed in abc123" }
```

`{"status": "open"}` reopens a resolved comment (clears the resolution note and resolving run). Resolving an already-resolved comment returns `404`.

### `DELETE /tasks/{id}/review-comments/{comment_id}`
Delete a comment entirely. Returns `204 No Content`.

### `GET /tasks/{id}/pr-url`
Returns `{ "url": "..." }` — a GitHub `compare` URL with the PR **title and body pre-filled** (task title, description, agent notes, and commit subjects). Open it to create a fully-described PR in one click; no GitHub auth or `gh` CLI needed. Requires the repo to have a GitHub remote and the task to have a provisioned branch (else `400`).

### `POST /tasks/{id}/pr`
One-click PR creation. Pushes the task's branch to origin, then runs `gh pr create` with the title from the task and a body assembled from the description, agent notes, and commit subjects. Stores the resulting PR URL and git state on the task and returns:
```json
{ "pr_url": "https://github.com/owner/repo/pull/42", "git_state": "pr_open" }
```
**Idempotent** — if a PR already exists for the branch, that PR is returned instead of erroring. Requires the repo to have a GitHub remote, the task to have a provisioned branch (else `400`), and the `gh` CLI to be authenticated (a `gh pr create` failure returns `502`).

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

### `PATCH /tasks/{id}/archive`
Archive or unarchive a task. Archived tasks are hidden from the default board
view (`GET /tasks` excludes them unless `archived=all|only` is passed),
excluded from the GitHub PR status sweep, and never picked up by the
dispatcher. Archiving does not change the task's `label`, so unarchiving
restores it exactly where it was.

```json
{ "archived": true }
```

Returns the updated `Task` object.

### `POST /tasks/bulk`
Apply one action to many tasks. Each task is processed independently — one
failure doesn't abort the rest.

```json
{
  "ids": ["uuid", "uuid"],
  "action": "move | pause | resume | archive | unarchive",
  "to_label": "required when action is move",
  "note": "optional transition note (move only)"
}
```

`move` transitions are validated through the workflow engine per task, exactly
like `PATCH /tasks/{id}/label`. Response is `200` if every task succeeded,
`207 Multi-Status` if any failed:

```json
{
  "results": [
    { "id": "uuid", "ok": true },
    { "id": "uuid", "ok": false, "error": "no transition defined between these labels" }
  ]
}
```

---

## Task Templates

Reusable pre-filled `title`/`description`/`type` for recurring shapes of work
("upgrade dependency X", "fix flaky test"). Templates only pre-fill the
new-task form — creating a task from a template is just `POST /tasks` with the
filled-in fields.

### `GET /templates`
List all templates, sorted by name.

### `POST /templates`
Create a template. `name` is required and unique (`409` on conflict); `type`
defaults to `feature`.

```json
{ "name": "Upgrade dependency", "title": "Upgrade <pkg> to latest", "description": "…", "type": "chore" }
```

### `GET /templates/{id}`
Get a single template.

### `PUT /templates/{id}`
Update a template (same body as create). `404` if missing, `409` on name conflict.

### `DELETE /templates/{id}`
Delete a template. Returns `204`.

---

## Agent Runs

### AgentRun Object

| Field | Type | Description |
|---|---|---|
| `id` | UUID | Run identifier |
| `task_id` | UUID | Associated task |
| `agent_config_id` | UUID | Agent config used for this run |
| `status` | string | `pending`, `running`, `completed`, `failed`, `waiting_human`, `cancelled` |
| `feedback` | string? | Feedback set on rejection (injected into the next run's prompt) |
| `stored_info` | string? | Info stored by the agent via `store_info`; visible in the task UI |
| `created_at` | RFC3339 | When the run was created |
| `started_at` | RFC3339? | When the run started executing |
| `completed_at` | RFC3339? | When the run finished |
| `input_tokens` | integer | Total input/prompt tokens consumed across the run (summed across all turns); `0` if the provider doesn't report usage |
| `output_tokens` | integer | Total output/completion tokens consumed across the run |
| `cost_usd` | number | Cost of the run in USD. Authoritative (CLI-reported) for `claude`/`qwen_code`; estimated from tokens via an internal pricing table for `anthropic`/`llm`; always `0` for `opencode`. See [agents.md § Cost & Usage Tracking](agents.md#cost--usage-tracking) |
| `session_id` | string | Provider-side conversation session for this run (claude/qwen stream-json `session_id`); used to resume the session on a later run (see [agents.md § Session Resume](agents.md#session-resume)). Empty when the provider has no session |

### `GET /tasks/{id}/runs`
List all agent runs for a task (newest first).

### `GET /tasks/{id}/runs/{run_id}`
Get a single run record.

### `POST /tasks/{id}/runs/{run_id}/cancel`
Stop a running agent run (kill switch). The pool cancels the run's context —
killing CLI provider subprocesses and aborting HTTP provider requests — then,
once the provider returns, marks the run `cancelled` (not `failed`, and without
consuming retry budget), **pauses the task** so it isn't immediately
re-dispatched, clears the active-run lock, and broadcasts `task.agent_done`.

Cancellation is asynchronous: a `202 Accepted` (`{ "status": "cancelling",
"run_id": "..." }`) means it was signalled, not that the run has fully stopped —
watch for the `task.agent_done` WebSocket event. Returns `409` if the run isn't
currently `running` (already finished, or racing to finish) and `404` if the run
doesn't belong to the task. Resume the task (unpause) or hit re-run to dispatch
again.

### `POST /tasks/{id}/runs/{run_id}/reply`
Answer a `waiting_human` run's `request_human` question with text and let the
agent continue. Body: `{ "message": "..." }`. Starts a **new run** that resumes
the prior provider session where supported (`claude`, unless the agent config
has `resume_sessions` off) so the reply lands as the next message of the same
conversation; other providers start cold with the reply injected into the
prompt under `RESPONSE FROM HUMAN`. The task stays on its current label — a
reply is a conversation, not a workflow transition — and the replied-to run
keeps its `waiting_human` status (matching approve/reject). The reply is
recorded at the top of the new run's log.

Returns `202` with `{ "run_id": "<new run>" }`, `400` for an empty message,
`404` if the run doesn't belong to the task, `409` if the run isn't the task's
active `waiting_human` run (or no enabled agent config can serve it), `503` if
the worker pool is saturated.

### `GET /tasks/{id}/runs/{run_id}/logs`
Get a page of a run's persisted log entries, in chronological order (oldest
first). A long verbose run can produce tens of thousands of entries, so the
endpoint is paginated.

Query params:

| Param | Meaning |
|---|---|
| `limit` | Page size (default 200, clamped to 1000) |
| `before` | Cursor to load earlier entries — the id of the oldest entry you already have |

Omit `before` to get the most recent page (the tail). When earlier entries
remain, the response includes `X-Has-More: true` and an `X-Prev-Cursor` header
whose value is the id to pass as `before` to load the previous page. This is the
"load earlier" path that complements the capped WebSocket log replay.

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
  "max_turns": 50,
  "max_retries": 3,
  "retry_backoff_secs": 30,
  "env": { "KEY": "value" }
}
```

`max_retries`/`retry_backoff_secs` configure auto-retry for *transient*
provider errors (rate limits, network blips, upstream 5xx) — see
[agents.md#retry-policy](agents.md#retry-policy). Both are optional on
create/update and default to `3`/`30`.

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
  "label_counts": { "plan": 5, "work": 3 },
  "active_agents": [
    { "run_id": "...", "task_id": "...", "task_title": "...", "agent_name": "...", "started_at": "..." }
  ],
  "intervention_queue": [
    { "run_id": "...", "task_id": "...", "task_title": "...", "message": null, "created_at": "..." }
  ],
  "cost_total": { "input_tokens": 12345, "output_tokens": 6789, "cost_usd": 0.42 },
  "cost_by_provider": [
    { "provider": "claude", "input_tokens": 12345, "output_tokens": 6789, "cost_usd": 0.42, "run_count": 10 }
  ],
  "claude_usage": {
    "available": true,
    "five_hour_percent": 42.5,
    "five_hour_resets_at": "2026-07-03T18:00:00Z",
    "weekly_percent": 12.0,
    "weekly_resets_at": "2026-07-10T00:00:00Z"
  }
}
```

- `label_counts` — number of tasks currently in each workflow label.
- `active_agents` — agent runs currently in progress.
- `intervention_queue` — runs in `waiting_human`, awaiting approve/reject.
- `cost_total` / `cost_by_provider` — aggregate token/cost usage across all
  runs in a terminal state (completed, failed, waiting_human), computed
  from data already recorded in this app's own database.
- `claude_usage` — **live** rate-limit utilization for the current Claude
  account, fetched directly from Anthropic's OAuth usage endpoint (distinct
  from `cost_total`, which is derived from local run records). `available`
  is `false` (with other fields zeroed/omitted) when the server has no
  Claude OAuth credentials (`~/.claude/.credentials.json`, from `claude
  login`) or the live fetch failed for any reason — this never causes the
  `/dashboard` request itself to fail. See
  [`docs/providers/claude.md`](providers/claude.md) for details on the
  credential requirement.

---

## Health

### `GET /healthz`
Returns `200 OK` with `{"status":"ok"}`. Not auth-gated. (Served at the server
root, **not** under `/api/v1`.)

### `GET /health/providers`
Provider / onboarding readiness checks. Surfaces first-run misconfiguration at a
glance instead of letting it show up as a failed agent run. Returns an ordered
list of checks:

```json
{
  "checks": [
    { "id": "claude_cli", "name": "Claude CLI", "status": "ok", "detail": "claude CLI installed and credentials found" },
    { "id": "mcp_sidecar", "name": "MCP sidecar", "status": "warn", "detail": "MCP_SERVER_PATH is not set", "hint": "Set MCP_SERVER_PATH to the mcp-server binary to enable signal_complete/request_human for claude/qwen agents." },
    { "id": "repo_base_dir", "name": "Repo base directory", "status": "error", "detail": "REPO_BASE_DIR is set but does not exist: /repos", "hint": "Create the directory or point REPO_BASE_DIR at an existing path." }
  ]
}
```

- `status` is `ok` (green — ready), `warn` (yellow — optional/degraded, or a
  credential we couldn't detect heuristically), or `error` (red — a required
  item is missing and runs using it will fail).
- `hint` is a one-line fix, present whenever `status` is not `ok`.
- Checks covered: the `claude` CLI (present + authenticated), API keys for the
  `anthropic`/`llm` providers, `qwen`/`opencode` binaries (only emitted for
  providers referenced by an **enabled** agent config), the MCP sidecar binary
  (`MCP_SERVER_PATH`), gh auth (same probe as `/github/auth-status`), and
  `REPO_BASE_DIR`.
- Checks are cheap and side-effect free (PATH lookups, credential/config-file
  existence, env/config values). No real agent invocation is made, so a green
  `claude` row means credentials were **found**, not that a live token was
  validated. Rendered by the frontend's **Health** page.
