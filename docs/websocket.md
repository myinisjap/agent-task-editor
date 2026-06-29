# WebSocket Protocol

The WebSocket endpoint provides real-time updates for task state changes and live agent log streaming.

## Connection

```
ws://host/ws[?token=<API_TOKEN>]
```

If `API_TOKEN` is set, you must pass it via the `token` query parameter. Browsers cannot set custom headers on WebSocket upgrade requests.

The connection is kept alive with a server-side ping every 25 seconds.

## Subscribing to Tasks

Send JSON messages to control which tasks you receive events for:

```json
{ "type": "subscribe", "task_id": "uuid" }
{ "type": "unsubscribe", "task_id": "uuid" }
```

Maximum 100 active subscriptions per connection.

**On subscribe**, the server immediately replays all persisted log entries for the task's current agent run. This ensures a reconnecting browser sees prior output without any gap.

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
    "from": "todo",
    "to": "in-progress",
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
    "status": "completed | failed | waiting_human"
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
The task's GitHub PR state changed (fired by the background GitHub sync).

```json
{
  "type": "task.git_state_changed",
  "payload": {
    "task_id": "uuid",
    "git_state": "pr_open | pr_merged | pr_closed | pushed | \"\""
  }
}
```

### `agent.log`
A single log entry from a running agent. Sent for every line of output in real time, and replayed from the database when a client subscribes.

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

## Client-Side Behaviour

The frontend `WSClient` (`frontend/src/api/ws.ts`) handles:

- **Auto-reconnect** with exponential back-off (1s → 2s → 4s → … up to 30s)
- **Re-subscribe** — all active subscriptions are re-sent after a reconnect
- **Event routing** — listeners registered per `task_id` receive matching `agent.log` entries; global listeners receive all other events
- **Token injection** — appends `?token=` automatically from `VITE_API_TOKEN` if set

## Hub Architecture

The server-side hub broadcasts to all connected clients that are subscribed to the relevant task. Events published by the pool (agent logs, status changes) and the workflow engine (label changes) flow through a single in-process channel. Each client has a 256-message send buffer; slow clients that fill their buffer have their connection dropped gracefully.

`agent.log` events with a `task_id` are only delivered to clients subscribed to that task. All other events (label changes, agent started/done, rate limited, etc.) are broadcast to all connected clients.
