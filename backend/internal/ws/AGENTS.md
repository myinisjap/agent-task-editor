# internal/ws

WebSocket hub and per-client connection management. Protocol reference: `docs/websocket.md`.

## Hub

```go
hub := ws.NewHub()
hub.Publish(eventType string, payload map[string]any)
```

`Publish` serialises JSON and delivers it to clients. `agent.log` carries a `task_id` and goes only to clients subscribed to that task; all other events broadcast to all clients. `*Hub` satisfies both `workflow.Publisher` and `agent.Publisher` — one hub instance is passed to both.

## Client Lifecycle (`ServeWS`)

1. **Auth** (when `API_TOKEN`/`API_TOKENS` is set): `?ticket=` only, checked with `hub.ConsumeTicket`. Tickets come from the bearer-gated `POST /api/v1/ws-ticket` (`ticket.go`, `handlers.WSTicketHandler`). A request with only `?token=` gets `401`. `/ws` is mounted outside `BearerAuth`. (ADR 0022)
2. Upgrade with `nhooyr.io/websocket`; the origin pattern list comes from `CORS_ORIGINS`.
3. Two goroutines under a shared context:
   - **Read pump**: parses `subscribe`/`unsubscribe`; max 100 subscriptions; spawns `replayTaskLogs` only for a *newly added* id (re-subscribing is a no-op); at most `maxConcurrentReplays` (4) in flight via `Client.replaySlots`, blocking rather than dropping.
   - **Write pump**: drains `c.send`, pings every 25s, and wraps every `conn.Write`/`conn.Ping` in its own 10s timeout (`writeTimeout`/`pingTimeout`) — never the shared context directly. When `Hub.Publish` drops an event for a full buffer it sets `Client.lagged` and nudges `Client.wake`; the pump sends one `{"type":"resync","payload":{}}` frame (`takeResync`) once `c.send` is empty. (ADR 0023)
4. Either pump cancels the shared context; `wg.Wait()` then graceful close.

## Ticket Auth (`ticket.go`)

```go
hub.IssueTicket() (string, error)   // called by POST /api/v1/ws-ticket
hub.ConsumeTicket(ticket string) bool
```

32 random bytes (`crypto/rand`, `base64.RawURLEncoding`), TTL 30s (`ticketTTL`; tests use `newTicketStoreWithTTL`). `issue()` sweeps expired entries; `consume()` always deletes on lookup so a ticket is truly single-use. Mutex-guarded map, no background goroutine.

## Log Replay

On subscribe, `replayTaskLogs` sends the newest `replayLimit` (500) entries of the task's `current_agent_run_id` run (via `ListAgentLogsPage`; `current_`, not `active_`, so finished runs replay — ADR 0001) as **one** `agent.log_replay` message (`entries` oldest-first, `run_id`, `task_id`, `has_more`). Earlier entries load via `GET /tasks/{id}/runs/{run_id}/logs?before=…`.

Live `agent.log` events published by `pool.go#persistLogs` carry the same `id` as the persisted `agent_logs` row that replay later returns. Keep that contract: clients dedupe the live/replay overlap by `id`.

## Send Buffer

256-message buffered channel per client. When full, publishes are dropped for that client (and a `resync` is queued); the connection is not closed.
