# 23. WebSocket delivery is bounded: drop-and-resync, tail replay, timeouts

Status: Accepted

## Context

A slow or half-open client must never stall the hub or leak. The earlier
per-row log replay pushed every persisted row through the 256-slot send buffer,
stalling the UI and dropping live events on long runs; a re-subscribing client
could spawn unbounded replay goroutines; and a half-open peer (laptop sleep,
NAT timeout) parked the write pump in `Write`/`Ping` forever, keeping the
client in `Hub.clients`.

## Decision

- Each client has a 256-message send buffer. When full, `Publish` drops the
  event, sets `Client.lagged`, and the write pump sends one
  `{"type":"resync"}` frame once the buffer drains. The connection stays open;
  there are no sequence numbers.
- On subscribe, `replayTaskLogs` sends the newest `replayLimit` (500) entries
  as a single `agent.log_replay` batch (`entries`, `run_id`, `task_id`,
  `has_more`); earlier entries load via `GET .../logs?before=`. Replay uses
  `current_agent_run_id` (ADR 0001), so finished runs replay too.
- Live `agent.log` events carry the same `id` as the persisted row so clients
  dedupe the live/replay overlap by id.
- Limits: 100 subscriptions per client; replay starts only for newly added ids;
  at most 4 concurrent replays per client (a semaphore that blocks rather than
  drops).
- Every `conn.Write`/`conn.Ping` uses its own 10s timeout; pings every 25s.

## Consequences

- Clients must handle `resync` by refetching state.
- Keep the `id` contract between `persistLogs` and `replayTaskLogs`.
