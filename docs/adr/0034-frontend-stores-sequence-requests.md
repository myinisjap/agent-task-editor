# 34. Frontend stores sequence requests and merge WebSocket updates

Status: Accepted

## Context

Stores combine REST fetches with WebSocket upserts. A slow earlier response
overwrote newer data (workflow editor, #332), and a task-list sweep clobbered
upserts/removes that landed mid-flight (#341).

## Decision

Zustand stores own server state and are updated from actions and from WS events.
Fetches carry a monotonic request id, and stale responses are dropped.
`stores/tasks.ts` preserves WS upserts and removes that arrive during a sweep.
The workflow page request-sequences per-workflow loads and guards Save against
stale YAML. The WS client reconnects with capped, jittered exponential backoff
and re-sends subscriptions on open.

## Consequences

- New stores that mix fetch and WS should follow the same pattern.
