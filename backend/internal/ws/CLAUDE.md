# internal/ws

WebSocket hub and per-client connection management.

## Hub

```go
hub := ws.NewHub()
hub.Publish(eventType string, payload map[string]any)
```

The hub maintains the set of connected clients. `Publish` serialises the event as JSON and delivers it to every client subscribed to the relevant task. The `agent.log` event carries a `task_id` in its payload and is routed only to subscribed clients; all other events are broadcast to all clients.

`Hub` satisfies both `workflow.Publisher` and `agent.Publisher` interfaces — a single hub instance is passed to both the workflow engine and the agent pool.

## Client Lifecycle

Each WebSocket connection is managed by `ServeWS`:

1. Token validation (if `API_TOKEN` set) — checked against `?token=` query param
2. WebSocket upgrade using `nhooyr.io/websocket`
3. Two goroutines started under a shared context:
   - **Read pump** — parses `subscribe`/`unsubscribe` messages; enforces max 100 subscriptions; spawns `replayTaskLogs` on new subscribe
   - **Write pump** — drains `c.send` channel and writes to connection; sends keepalive pings every 25s
4. Either pump cancels the shared context on error/close
5. `wg.Wait()` then graceful close

## Log Replay

On subscribe, `replayTaskLogs` fetches all persisted log entries for the task's current agent run and queues them to `c.send`. This ensures clients that reconnect mid-run (or open the task detail page after a run started) see the full output.

Replay uses `task.current_agent_run_id` (not `active_agent_run_id`) — so clients can also replay logs from completed or failed runs.

## Send Buffer

Each client has a 256-message buffered channel. If a client is too slow to consume messages and the buffer fills, subsequent publishes drop the message for that client. The connection is not forcibly closed — backpressure is absorbed by dropping rather than blocking.

## CORS for WebSocket

`ServeWS` builds an origin pattern list from the same `CORS_ORIGINS` config used by the HTTP middleware, and passes it to `websocket.AcceptOptions`. This enforces the same origin policy for WS upgrades.
