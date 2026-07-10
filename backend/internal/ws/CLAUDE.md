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

1. Auth (if `API_TOKEN` set) — ticket-first, token-fallback:
   - `?ticket=` is checked first via `hub.ConsumeTicket`. Tickets are minted by the
     bearer-gated `POST /api/v1/ws-ticket` (see `ticket.go` / `handlers.WSTicketHandler`),
     are random (`crypto/rand`), single-use, and expire after ~30s. This keeps the
     long-lived API token out of the URL — query strings leak into reverse-proxy/access
     logs and browser history.
   - `?token=` is a **deprecated fallback**, checked via constant-time compare against
     `API_TOKEN`, kept for existing setups. A warning is logged whenever it's used.
2. WebSocket upgrade using `nhooyr.io/websocket`
3. Two goroutines started under a shared context:
   - **Read pump** — parses `subscribe`/`unsubscribe` messages; enforces max 100 subscriptions; spawns `replayTaskLogs` on new subscribe
   - **Write pump** — drains `c.send` channel and writes to connection; sends keepalive pings every 25s
4. Either pump cancels the shared context on error/close
5. `wg.Wait()` then graceful close

## Ticket Auth (`ticket.go`)

`ticketStore` (unexported, held on `Hub.tickets`) issues and validates the tickets used above:

```go
hub.IssueTicket() (string, error) // called by POST /api/v1/ws-ticket
hub.ConsumeTicket(ticket string) bool
```

- `issue()` generates 32 random bytes via `crypto/rand` and encodes them with `base64.RawURLEncoding` (query-string safe). It also opportunistically sweeps expired entries on each call so the map can't grow unbounded if tickets are minted but never consumed.
- `consume()` always deletes the ticket on lookup — whether or not it was found or still valid — so replay of a used or expired ticket always fails (true single-use).
- Default TTL is 30s (`ticketTTL`); tests can override it via `newTicketStoreWithTTL`.
- The store is a plain `sync.Mutex`-guarded map — fine for expected volume (single-user/self-hosted); no background sweep goroutine.

## Log Replay

On subscribe, `replayTaskLogs` fetches the **tail** of the task's current agent run's log (the newest `replayLimit` = 500 entries, via `ListAgentLogsPage`) and sends it as a **single batched `agent.log_replay` message**, not one `agent.log` per row. This ensures clients that reconnect mid-run (or open the task detail page after a run started) see prior output, while bounding the work: the previous per-row replay queued *every* persisted row through the 256-slot send buffer, which stalled the UI and could drop live events on a long run. The batch payload carries `entries` (chronological, oldest first), `run_id`, `task_id`, and `has_more`; when `has_more` is true the client loads earlier entries on demand via `GET /tasks/{id}/runs/{run_id}/logs?before=…`.

Replay uses `task.current_agent_run_id` (not `active_agent_run_id`) — so clients can also replay logs from completed or failed runs.

## Send Buffer

Each client has a 256-message buffered channel. If a client is too slow to consume messages and the buffer fills, subsequent publishes drop the message for that client. The connection is not forcibly closed — backpressure is absorbed by dropping rather than blocking.

## CORS for WebSocket

`ServeWS` builds an origin pattern list from the same `CORS_ORIGINS` config used by the HTTP middleware, and passes it to `websocket.AcceptOptions`. This enforces the same origin policy for WS upgrades.
