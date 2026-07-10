# WebSocket Protocol

The WebSocket endpoint provides real-time updates for task state changes and live agent log streaming.

## Connection

Browsers cannot set custom headers on a WebSocket upgrade request, so auth travels via the query string instead. If `API_TOKEN` is set, connect in two steps:

1. `POST /api/v1/ws-ticket` (normal Bearer auth) — mints a random, single-use ticket valid for ~30s:
   ```json
   { "ticket": "opaque-random-string", "expires_in": "30s" }
   ```
2. Open the socket with that ticket:
   ```
   ws://host/ws?ticket=<ticket>
   ```
   The ticket is consumed on first use — a replayed or expired ticket is rejected with `401`.

This keeps the long-lived `API_TOKEN` out of the WebSocket URL, since query strings are commonly captured by reverse-proxy access logs and browser history.

**Deprecated fallback:** `ws://host/ws?token=<API_TOKEN>` still works (checked with a constant-time compare) for existing setups that haven't migrated, but it puts the durable token in the URL and may be removed in a future release. Prefer the ticket flow above. Each use of the fallback is logged server-side as a warning.

The connection is kept alive with a server-side ping every 25 seconds.

## Subscribing to Tasks

Send JSON messages to control which tasks you receive events for:

```json
{ "type": "subscribe", "task_id": "uuid" }
{ "type": "unsubscribe", "task_id": "uuid" }
```

Maximum 100 active subscriptions per connection.

**On subscribe**, the server immediately replays the tail of the persisted log for the task's current agent run as a single batched `agent.log_replay` message (see below). This ensures a reconnecting browser sees prior output without any gap. Only the most recent entries are replayed (capped at 500); if the run is longer, `has_more` is `true` and earlier entries can be loaded on demand via `GET /tasks/{id}/runs/{run_id}/logs?before=…`.

## Event Types

All events have the shape:

```json
{ "type": "event.type", "payload": { ... } }
```

### `task.label_changed`
A task moved to a new label.

```json
{
  "type": "task.label_changed",
  "payload": {
    "task_id": "uuid",
    "from": "plan",
    "to": "review-plan",
    "note": "optional note"
  }
}
```

### `task.agent_started`
An agent run began.

```json
{
  "type": "task.agent_started",
  "payload": {
    "task_id": "uuid",
    "run_id": "uuid",
    "agent_name": "My Agent"
  }
}
```

### `task.agent_done`
An agent run completed (any terminal run status).

```json
{
  "type": "task.agent_done",
  "payload": {
    "task_id": "uuid",
    "run_id": "uuid",
    "status": "completed | failed | waiting_human | cancelled"
  }
}
```

### `task.needs_human`
The agent called `request_human` and is waiting for input.

```json
{
  "type": "task.needs_human",
  "payload": {
    "task_id": "uuid",
    "run_id": "uuid",
    "message": "Please review the schema changes before I continue."
  }
}
```

### `task.rate_limited`
An agent run was rate-limited by the provider and will be retried automatically.

```json
{
  "type": "task.rate_limited",
  "payload": {
    "task_id": "uuid",
    "run_id": "uuid",
    "agent_config_id": "uuid",
    "unblocked_at": "RFC3339 timestamp when retries resume"
  }
}
```

### `task.git_state_changed`
The task's GitHub PR state changed (fired by the background GitHub sync). When
`git_state` transitions to `pr_merged`, the sync also removes any leftover
worktree and deletes the task's local branch from the repo's main clone
(best-effort; the remote branch on `origin` is left untouched).

```json
{
  "type": "task.git_state_changed",
  "payload": {
    "task_id": "uuid",
    "git_state": "pr_open | pr_merged | pr_closed | pushed | \"\"",
    "pr_url": "https://github.com/org/repo/pull/123"
  }
}
```

### `task.created`
A new task was created by the background GitHub Issues importer (see
[task-sources.md](task-sources.md)). The payload is a subset of task fields —
clients should refetch the task for full data.

```json
{
  "type": "task.created",
  "payload": {
    "id": "uuid",
    "title": "Fix crash on empty input",
    "label": "not_ready",
    "repo_id": "uuid",
    "source": "github",
    "source_ref": "owner/repo#123"
  }
}
```

### `agent.log`
A single log entry from a running agent. Sent for every line of output in real time.

```json
{
  "type": "agent.log",
  "payload": {
    "run_id": "uuid",
    "task_id": "uuid",
    "entry": {
      "type": "stdout | stderr | system | tool_call | tool_result",
      "content": "string",
      "at": "RFC3339 timestamp"
    }
  }
}
```

### `agent.log_replay`
Sent once when a client subscribes to a task: the tail of the current run's
persisted log, batched into a single message rather than one `agent.log` per
row. This bounds the work (and send-buffer pressure) when subscribing to a task
with a very long run. `entries` is chronological (oldest first) and capped at
the 500 most recent rows; `has_more` is `true` when earlier entries exist and
can be fetched via `GET /tasks/{id}/runs/{run_id}/logs?before=…`.

```json
{
  "type": "agent.log_replay",
  "payload": {
    "run_id": "uuid",
    "task_id": "uuid",
    "has_more": false,
    "entries": [
      {
        "id": "uuid",
        "type": "stdout | stderr | system | tool_call | tool_result",
        "content": "string",
        "at": "RFC3339 timestamp"
      }
    ]
  }
}
```

## Client-Side Behaviour

The frontend `WSClient` (`frontend/src/api/ws.ts`) handles:

- **Auto-reconnect** with exponential back-off (1s → 2s → 4s → … up to 30s)
- **Re-subscribe** — all active subscriptions are re-sent after a reconnect
- **Event routing** — listeners registered per `task_id` receive matching `agent.log` entries; global listeners receive all other events
- **Ticket fetch** — if `VITE_API_TOKEN` is set, `connect()` first `POST`s `/api/v1/ws-ticket` (Bearer-authed) and opens the socket with the returned `?ticket=`; if that fetch fails it falls through and connects without a ticket (the server 401s and the reconnect loop retries)

## Hub Architecture

The server-side hub broadcasts to all connected clients that are subscribed to the relevant task. Events published by the pool (agent logs, status changes) and the workflow engine (label changes) flow through a single in-process channel. Each client has a 256-message send buffer; slow clients that fill their buffer have their connection dropped gracefully.

`agent.log` events with a `task_id` are only delivered to clients subscribed to that task. All other events (label changes, agent started/done, rate limited, etc.) are broadcast to all connected clients.
