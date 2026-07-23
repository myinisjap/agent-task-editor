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
| `priority` | integer | Dispatch priority: `-1`=low, `0`=normal (default), `1`=high, `2`=urgent. `ListAgentPickupTasks` orders eligible tasks by priority desc, then oldest first — see [agents.md#task-priority](agents.md#task-priority) |
| `queue_position` | integer? | Derived, read-time 0-based rank in the current agent-pickup queue (priority desc, then oldest first). Only set when the task is eligible for dispatch **and** the worker pool has no free slot (all `MAX_WORKERS` busy); `null` when the task isn't pickup-eligible or the pool has idle capacity |

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
  "workflow_id": "uuid (required)",
  "label": "string (optional, default not_ready)",
  "priority": "-1 | 0 | 1 | 2 (optional, default 0)"
}
```

**Multipart form** (`Content-Type: multipart/form-data`):
Same fields as form values, plus `attachments` (multiple file fields). Images are validated (max 10 MB each, image/* MIME type only) and stored in `UPLOAD_DIR`.

New tasks default to the `not_ready` label. Pass `label` to place a task
directly on any column defined in the workflow (e.g. `work` to make it
immediately agent-eligible). Because this is initial placement rather than a
state-machine transition, it is not restricted to the workflow's transition
edges; an unknown label returns `400`. The [board MCP server](board-mcp.md)
uses this to create tickets straight into `work` from a chat client.

### `GET /tasks/{id}`
Get a single task.

### `PATCH /tasks/{id}`
Update task fields (title, description, type, repo_id, max_cost_usd, priority).
`max_cost_usd` is an advisory per-task cost budget cap in USD (optional,
defaults to 0/unlimited if omitted the field is preserved from the
existing value) — see [agents.md#cost-budgets](agents.md#cost-budgets).
`priority` is one of `-1` (low), `0` (normal), `1` (high), `2` (urgent);
omitted preserves the existing value — see
[agents.md#task-priority](agents.md#task-priority).

### `DELETE /tasks/{id}`
Delete a task and all associated runs/logs. Also tears down the per-task git worktree and removes uploaded attachments.

### `PATCH /tasks/{id}/label`
Move the task to a different label via the workflow engine. Goes through normal transition validation — `to_label` must be a valid transition from the current label.

```json
{ "to_label": "label-name", "note": "optional note" }
```

Returns `400` if no transition exists, `403` if the transition requires human auth or the target label is `agent_ignore`.

### `GET /tasks/{id}/label-history`
Returns the task's label-transition audit trail (`task_label_history`), oldest first:

```json
[
  {
    "id": "...",
    "task_id": "...",
    "from_label": "in_review",
    "to_label": "done",
    "trigger": "human",
    "actor_id": "alice",
    "note": "looks good",
    "created_at": "2026-01-01T00:00:00Z"
  }
]
```

`actor_id` is the resolved named-token actor (see `API_TOKENS` in
[getting-started.md](getting-started.md)) for human-triggered transitions —
`null`/empty when the legacy single shared `API_TOKEN` (or no auth) was used,
since that token has no associated name. For agent-triggered transitions,
`actor_id` is the agent run ID. `from_label` is `null` for the task's initial
label assignment.

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

## Task Dependencies

Peer task dependencies are a pure dispatch gate: a task with an unsatisfied
blocker is never picked up by the dispatcher, but humans can still move it
anywhere. Blocked-ness is derived at read time from the blocker's
label/archived state — it is never stored. Adding or removing an edge
publishes `task.updated` (see [websocket.md](websocket.md)) for both affected
tasks.

### `GET /tasks/{id}/dependencies`
List a task's dependency edges in both directions.

```json
{
  "blocked_by": [
    { "task_id": "uuid", "title": "...", "label": "...", "archived": false, "satisfied": true }
  ],
  "blocking": [
    { "task_id": "uuid", "title": "...", "label": "...", "archived": false, "satisfied": false }
  ],
  "blocked_by_count": 1,
  "blocking_count": 1
}
```

`blocked_by_count` is the number of *unsatisfied* blockers. `satisfied` on
`blocking` entries is always `false` — it isn't meaningful from this task's
perspective (a dependent's satisfaction is relative to its own other
blockers), the field is only present for shape symmetry with `blocked_by`.

### `POST /tasks/{id}/dependencies`
Add a dependency edge: this task depends on (is blocked by) `depends_on_task_id`.

```json
{ "depends_on_task_id": "uuid" }
```

Returns `204 No Content`. `400` if the two tasks are the same task, are in
different workflows, or the blocker's workflow has no terminal label (such an
edge could never be satisfied). `409` if the edge already exists, or if adding
it would create a cycle — the error message includes the cycle path, e.g.
`"dependency would create a cycle: A → B → C → A"`.

### `DELETE /tasks/{id}/dependencies/{dep_id}`
Remove a dependency edge. Idempotent — returns `204` even if the edge didn't
exist.

---

## Subtasks

Agent-driven child tasks (the `create_subtask` MCP tool posts here live during
a planning run so children appear on the board mid-run and the agent gets real
task ids back). Humans can also call this directly.

### `POST /tasks/{id}/subtasks`
Create a child task under the parent named in the path.

```json
{
  "title": "string (required)",
  "description": "string",
  "type": "feature | bug | ...",
  "label": "optional agent_ignore label override"
}
```

- Depth limit 1 — a subtask cannot itself have subtasks (`400` if the parent
  is itself a subtask).
- The child lands on the workflow's first `agent_ignore` (human-gate) label by
  default, or the first label overall if the workflow has none. `label` can
  override this, but only to another `agent_ignore` label (`400` otherwise).
- Per-parent subtask cap: `10` by default, or the creating agent config's
  `max_subtasks` if the config that produced the parent's active run has
  `subtasks_enabled` and sets `max_subtasks` (`403` if `subtasks_enabled` is
  off for that config). Returns `422` if the cap is already reached.
- Auto-creates a parent→child dependency edge (see Task Dependencies above) so
  the parent can't be dispatched again until every child finishes.
- Publishes `task.created` (with `parent_id`) and `task.updated` (for the
  parent) — see [websocket.md](websocket.md).

Returns `201` with the created task object (same shape as `POST /tasks`).

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

## Task Schedules

Recurring instantiation of a task template against a repo on a cron
expression. A background sweep fires due, enabled schedules and creates a
task, skipping the firing while an open task from a prior firing of the same
schedule still exists. See [task-templates.md](task-templates.md) for the
full behavior (dedup semantics, cron format, `not_ready` vs. unattended
agent-label targets).

### `GET /schedules`
List all schedules.

### `POST /schedules`
Create a schedule. `template_id`, `repo_id`, and `cron_expr` are required.
`400` if `cron_expr` fails to parse, if `template_id`/`repo_id` is missing,
if the repo has no workflow assigned, or if `target_label` isn't one of that
workflow's labels; `404` if `template_id` or `repo_id` doesn't exist.
`target_label` defaults to `not_ready`; `enabled` defaults to `true`.

```json
{ "template_id": "uuid", "repo_id": "uuid", "cron_expr": "0 6 * * 1", "target_label": "not_ready", "enabled": true }
```

### `GET /schedules/{id}`
Get a single schedule.

### `PUT /schedules/{id}`
Update a schedule's `cron_expr`/`target_label`/`enabled` (template/repo are
immutable after creation). `400` on invalid `cron_expr` or a `target_label`
that isn't one of the schedule's repo's workflow labels; `404` if missing.

### `DELETE /schedules/{id}`
Delete a schedule. Returns `204`.

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
| `cost_usd` | number | Cost of the run in USD. Authoritative (CLI-reported) for `claude`/`qwen_code`; estimated from tokens against the user-editable pricing table (falling back to an internal hardcoded table) for `anthropic`/`llm`; always `0` for `opencode`. See [agents.md § Cost & Usage Tracking](agents.md#cost--usage-tracking) |
| `cost_unknown` | integer | `1` if tokens were consumed but no pricing table row matched the model, so `cost_usd` was left at `0` as a placeholder rather than a computed figure. `0` otherwise — including for `claude`/`qwen_code`, whose `cost_usd` (even a legitimate `0` under a Claude Max subscription) is always authoritative. |
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
  "max_cost_usd": 0,
  "env": { "KEY": "value" }
}
```

`max_retries`/`retry_backoff_secs` configure auto-retry for *transient*
provider errors (rate limits, network blips, upstream 5xx) — see
[agents.md#retry-policy](agents.md#retry-policy). Both are optional on
create/update and default to `3`/`30`.

`max_cost_usd` is an advisory per-task cost budget cap in USD, checked by
the dispatcher before each sweep-dispatch against the task's cumulative
recorded run cost — see [agents.md#cost-budgets](agents.md#cost-budgets).
Optional on create/update, defaults to `0` (unlimited). Tasks can also carry
their own `max_cost_usd` (see `PATCH /tasks/{id}` below); when both are set
the lower of the two applies.

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

### `GET /agents/claude-options`
Returns the Claude plugins and user-level MCP servers discovered on this
machine (from `~/.claude/plugins/installed_plugins.json` and the global
`mcpServers` key in `~/.claude.json`), for the frontend to present as
per-agent-config selection options. Claude-provider-specific for now; other
providers have no equivalent.

```json
{
  "plugins": [{ "id": "string", "name": "string", "marketplace": "string" }],
  "mcp_servers": ["string"]
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

### `PATCH /repos/{id}`
Partial update. All fields are optional and merge with the repo's existing
values; setting `remote_url` or `workflow_id` to an empty string clears it.

```json
{
  "name": "string",
  "path": "/absolute/path/to/repo",
  "remote_url": "string|null",
  "workflow_id": "string|null",
  "issue_sync_enabled": true,
  "issue_sync_label": "string",
  "issue_writeback_enabled": true,
  "pr_review_auto_transition_enabled": true
}
```

`pr_review_auto_transition_enabled` (requires `remote_url`): when set,
`internal/ghsync` automatically transitions a task along its workflow's
"failure" human path (same target as a manual Reject) the first time a sweep
ingests new PR review feedback for it — a `changes_requested` review, a new
inline review comment, or a newly-failing GitHub Actions check. Off by
default; feedback is always ingested and surfaced in the next run's prompt
regardless of this flag. See [task-sources.md](task-sources.md).

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
  "agent_config_stats": [
    {
      "agent_config_id": "...",
      "agent_name": "opus-on-review",
      "provider": "claude",
      "run_count": 42,
      "completed_count": 35,
      "failed_count": 5,
      "waiting_human_count": 2,
      "success_rate_percent": 83.3,
      "avg_duration_secs": 187.4,
      "p90_duration_secs": 412.0,
      "avg_turns_to_done": 1.6,
      "avg_transient_retries": 0.3,
      "tasks_with_retries": 4,
      "input_tokens": 512345,
      "output_tokens": 98765,
      "cost_usd": 3.87
    }
  ],
  "cost_by_day": [
    { "day": "2026-07-07", "input_tokens": 45678, "output_tokens": 12345, "cost_usd": 0.31, "run_count": 6 }
  ],
  "cost_by_task": [
    { "task_id": "...", "task_title": "Refactor auth flow", "input_tokens": 89012, "output_tokens": 23456, "cost_usd": 0.58 }
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
- `agent_config_stats` — per-agent-config run analytics, sorted by
  `run_count` descending, so you can see which model/provider/agent config
  is actually performing rather than guessing. For each agent config still
  present in the database it aggregates: completed/failed/waiting_human
  counts and the resulting `success_rate_percent`; average and p90 run
  duration (`avg_duration_secs` / `p90_duration_secs`, seconds); average
  "turns to done" per task (`avg_turns_to_done` — how many runs a task
  needed before reaching a terminal label); a transient-retry snapshot
  (`avg_transient_retries`, `tasks_with_retries`); and token/cost totals.
  Only runs in a terminal state with a still-existing `agent_config_id` are
  included — same filtering as `cost_by_provider` (a run whose agent config
  was later deleted has `agent_config_id` set `NULL` and can no longer be
  attributed to any config). Two important caveats:
  1. **Last-run attribution**: `avg_turns_to_done`,
     `avg_transient_retries`, and `tasks_with_retries` are all computed by
     attributing a *whole task* to the agent config of that task's **last**
     run, not by proportionally splitting the task across every config it
     passed through. A task retried under agent A and then finished by
     agent B has all of its turns/retries counted only toward B.
  2. **Live, resettable retry snapshot**: the retry fields read
     `tasks.transient_retry_count` as it stands *right now* for tasks
     currently sitting on a terminal label. That counter resets to `0` on
     success or escalation to a human, so these numbers are a live snapshot
     of "how many done tasks currently have a nonzero retry count", **not**
     a lifetime/historical count of every transient retry that ever
     happened for that agent config.
- `claude_usage` — **live** rate-limit utilization for the current Claude
  account, fetched directly from Anthropic's OAuth usage endpoint (distinct
  from `cost_total`, which is derived from local run records). `available`
  is `false` (with other fields zeroed/omitted) when the server has no
  Claude OAuth credentials (`~/.claude/.credentials.json`, from `claude
  login`) or the live fetch failed for any reason — this never causes the
  `/dashboard` request itself to fail. See
  [`docs/providers/claude.md`](providers/claude.md) for details on the
  credential requirement.
- `cost_by_day` — daily token/cost/run-count rollup, most recent day first,
  capped at the last 30 days with recorded activity. Same terminal-status
  filtering (`completed`/`failed`/`waiting_human`) as `cost_total`.
- `cost_by_task` — the 20 highest-cost tasks by cumulative `cost_usd`.
  Unlike `cost_total`/`cost_by_provider`/`agent_config_stats`, this
  includes runs in **every** status (not just terminal ones), matching the
  same filtering the dispatcher's cost-budget guard uses — see
  [agents.md#cost-budgets](agents.md#cost-budgets).

### `GET /dashboard/cost-by-task`
Returns the full per-task cost rollup (no top-20 cap, no `task_title`) as a
flat array: `[{ "task_id": "...", "input_tokens": 0, "output_tokens": 0,
"cost_usd": 0.0 }]`. Same every-status filtering as `Dashboard.cost_by_task`
above. Backs the Board page's "Filtered cost" badge, which needs a cost
figure for every currently-visible task rather than just the top 20.

---

## Health

### `GET /healthz`
Liveness probe. Returns `200 OK` with `{"status":"ok","version":"<version>"}`.
`version` is the running build's version: `"dev"` for local/unstamped builds,
or the release tag (e.g. `"v1.4.0"`) for GHCR images, stamped at build time
via `-ldflags "-X main.Version=<tag>"` (see `backend/Dockerfile`'s `VERSION`
build-arg and `.github/workflows/release.yml`). (Served at the server root,
**not** under `/api/v1`, and mounted **outside** `API_TOKEN`/`API_TOKENS`
bearer auth — like `/metrics`, it's intentionally unauthenticated so
container/orchestrator healthchecks (see `docker-compose.yml`) work without
needing to inject the token. It returns only a static status/version and
leaks no sensitive data.)

### `GET /health/providers`
Provider / onboarding readiness checks. Surfaces first-run misconfiguration at a
glance instead of letting it show up as a failed agent run. Returns an ordered
list of checks:

```json
{
  "checks": [
    { "id": "claude_cli", "name": "Claude CLI", "status": "ok", "detail": "claude CLI installed and credentials found" },
    { "id": "mcp_sidecar", "name": "MCP sidecar", "status": "warn", "detail": "MCP_SERVER_PATH is not set", "hint": "Set MCP_SERVER_PATH to the mcp-server binary to enable signal_complete/request_human for claude/qwen agents." },
    { "id": "repo_base_dir", "name": "Repo base directory", "status": "error", "detail": "REPO_BASE_DIR is set but does not exist: /repos", "hint": "Create the directory or point REPO_BASE_DIR at an existing path." },
    { "id": "version", "name": "Version", "status": "ok", "detail": "running v1.4.0" },
    { "id": "update_check", "name": "Update available", "status": "warn", "detail": "update available: v1.5.0 (running v1.4.0)", "hint": "https://github.com/myinisjap/agent-task-editor/releases" }
  ]
}
```

- `status` is `ok` (green — ready), `warn` (yellow — optional/degraded, or a
  credential we couldn't detect heuristically), or `error` (red — a required
  item is missing and runs using it will fail).
- `hint` is a one-line fix, present whenever `status` is not `ok`.
- Checks covered: the `claude` CLI (present + authenticated), API keys for the
  `anthropic`/`llm` providers, `qwen`/`opencode` binaries (only emitted for
  providers actually referenced by an **enabled** agent config or a chat
  session, via their Provider Config — an unused/disabled Provider Config
  doesn't produce a check), the MCP sidecar binary
  (`MCP_SERVER_PATH`), gh auth (same probe as `/github/auth-status`),
  `REPO_BASE_DIR`, `auto_backup` (whether the automatic local-snapshot
  scheduler is enabled via `BACKUP_DIR` — see [backup.md](backup.md)), and
  `version` (the running build's version — see `GET /healthz` above).
- Checks are cheap and side-effect free (PATH lookups, credential/config-file
  existence, env/config values). No real agent invocation is made, so a green
  `claude` row means credentials were **found**, not that a live token was
  validated. Rendered by the frontend's **Health** page.
- `update_check` is an **opt-in** row (env var `UPDATE_CHECK_ENABLED=true` /
  YAML `update_check_enabled: true`, default `false`) that shells out to
  `gh release view` to compare the running version against the latest
  published GitHub release tag. Disabled by default so the app never "phones
  home" without the operator explicitly enabling it. It is best-effort and
  bounded by a short timeout: if `gh` is unavailable, unauthenticated, or
  there's no network, it degrades to `warn` ("could not check for updates")
  rather than `error`, and never blocks or fails the rest of the response.

### `GET /metrics`
Prometheus text-exposition-format metrics for scraping (served at the server
root, **not** under `/api/v1`). Not gated by `API_TOKEN` — independently
gated by the optional `METRICS_TOKEN` env var (unset by default, i.e.
unauthenticated, since most Prometheus scrape configs can't easily carry a
second, endpoint-specific token). When `METRICS_TOKEN` is set, requests must
carry `Authorization: Bearer $METRICS_TOKEN`.

```bash
curl http://localhost:8080/metrics
# or, if METRICS_TOKEN is set:
curl -H "Authorization: Bearer $METRICS_TOKEN" http://localhost:8080/metrics
```

In addition to the standard Go runtime/process collectors (`go_*`,
`process_*`), the following application metrics are exposed:

**Dispatcher / pool**
- `ate_dispatch_eligible_tasks` (gauge) — tasks eligible for pickup on the most recent sweep.
- `ate_dispatched_runs_total` (counter) — runs successfully started by the dispatcher.
- `ate_pool_queue_depth` (gauge) — jobs currently queued in the worker pool.
- `ate_pool_busy_workers` (gauge) — workers currently running a job.
- `ate_pool_max_workers` (gauge) — configured `MAX_WORKERS`.
- `ate_pool_submit_rejected_total` (counter) — jobs dropped because the queue was full.

**Runs**
- `ate_run_terminal_total{status}` (counter) — runs by terminal status (`completed`/`failed`/`cancelled`/`waiting_human`).
- `ate_run_classification_total{classification}` (counter) — failed runs by classification (`genuine`/`transient`/`rate_limit`/`auth`).
- `ate_run_duration_seconds{provider}` (histogram) — run duration from start to terminal outcome.

**Cost / tokens**
- `ate_run_cost_usd_total{provider,agent_config_name}` (counter).
- `ate_run_input_tokens_total{provider,agent_config_name}` (counter).
- `ate_run_output_tokens_total{provider,agent_config_name}` (counter).

**WebSocket**
- `ate_ws_connected_clients` (gauge) — currently connected WS clients.
- `ate_ws_broadcast_dropped_total` (counter) — events dropped due to a full client send buffer.

**Sync loops**
- `ate_ghsync_sweep_duration_seconds` (histogram) — GitHub PR-status sweep duration.
- `ate_tasksource_sweep_duration_seconds` (histogram) — GitHub issue-import sweep duration.
- `ate_gh_calls_total{command}` (counter) — `gh` CLI invocations by logical command (`pr_list`, `pr_create`, `issue_list`, `auth_status`, `branch_check`, `issue_label_add`, `issue_comment`, `issue_close`), an early warning signal for GitHub API rate limiting.

---

## Backup

### `GET /backup`
Streams a consistent point-in-time snapshot of the SQLite database as
`application/octet-stream`, generated via SQLite's `VACUUM INTO` (not a raw
file copy), so it's safe to call even while the app is under active write
load. Requires the same Bearer auth as the rest of `/api/v1`.

```bash
curl -H "Authorization: Bearer $API_TOKEN" http://localhost:8080/api/v1/backup -o backup-$(date +%F).db
```

The frontend's **Health** page also has a "Download backup" button that hits
this endpoint for one-click on-demand snapshots. See
[backup.md](backup.md) for the full restore procedure, the optional
`BACKUP_DIR`/`BACKUP_INTERVAL`/`BACKUP_KEEP` automatic local-snapshot
scheduler, and a Litestream sidecar example for continuous offsite
replication.

### `GET /backup/settings` / `PUT /backup/settings`
Reads/updates the DB-backed interval (`interval_seconds`) and retention
count (`keep`) for the automatic local-backup scheduler. Changes take effect
on the scheduler's next scheduled run without a restart. `interval_seconds`
must be at least `600` (10 minutes); `keep` must be at least `1`. Defaults
to `86400` (once a day) / `7`, matching the scheduler's previous
env-var-only defaults. Whether the scheduler is enabled at all remains a
deploy-time-only choice (`BACKUP_DIR`) — this endpoint only controls how
often it runs and how many snapshots it keeps once enabled.

```bash
curl -H "Authorization: Bearer $API_TOKEN" http://localhost:8080/api/v1/backup/settings
curl -X PUT -H "Authorization: Bearer $API_TOKEN" -H "Content-Type: application/json" \
  -d '{"interval_seconds": 3600, "keep": 7}' \
  http://localhost:8080/api/v1/backup/settings
```

The frontend's **Health** page has an "Automatic backup schedule" form that
calls these endpoints. See [backup.md](backup.md#changing-the-intervalretention-count-at-runtime).

### `GET /settings/pricing` / `PUT /settings/pricing`
Reads/replaces the user-editable per-model USD-per-1M-token pricing table
used to estimate `anthropic`/`llm` run costs (see [agents.md § Editable
pricing table](agents.md#editable-pricing-table)). `GET` returns every row,
ordered by model. `PUT` replaces the *entire* table with the submitted array
in a single transaction — add/remove/edit a model are all expressed
client-side as a new full list. Rejects an empty or duplicate `model`, or a
negative `input_per_1m`/`output_per_1m`, with `400`. Takes effect on the very
next `anthropic`/`llm` run completion without a restart. A model not listed
here falls back to a small hardcoded table; a model matching neither has
that run's `cost_unknown` flag set to `1` instead of a silent `$0` (see the
`AgentRun` object above).

```bash
curl -H "Authorization: Bearer $API_TOKEN" http://localhost:8080/api/v1/settings/pricing
curl -X PUT -H "Authorization: Bearer $API_TOKEN" -H "Content-Type: application/json" \
  -d '[{"model": "claude-sonnet-4-5", "input_per_1m": 3, "output_per_1m": 15}]' \
  http://localhost:8080/api/v1/settings/pricing
```

The frontend's **Configuration → Pricing** page provides an editable table
UI (add/remove rows, Save) for this endpoint.

---

## WebSocket Auth

### `POST /ws-ticket`
Mints a random, single-use ticket for authenticating the `GET /ws` upgrade
without putting the long-lived `API_TOKEN` in the URL (query strings are
commonly captured by reverse-proxy access logs and browser history). Requires
the same Bearer auth as the rest of `/api/v1` — minting a ticket already
requires holding the token. No request body.

```bash
curl -X POST -H "Authorization: Bearer $API_TOKEN" http://localhost:8080/api/v1/ws-ticket
```

```json
{ "ticket": "opaque-random-string", "expires_in": "30s" }
```

The ticket is valid for ~30 seconds and is consumed on first use — connect
with `ws://host/ws?ticket=<ticket>` before it expires; a replayed or expired
ticket is rejected with `401`. See [websocket.md](websocket.md) for the full
connection flow, including the deprecated `?token=` fallback.
