# 22. WebSocket auth uses single-use tickets; `/ws` is mounted outside bearer middleware

Status: Accepted

## Context

Browsers cannot set headers on a WebSocket handshake. Putting the long-lived
API token in `?token=` leaks it into reverse-proxy logs and browser history.
An earlier `Upgrade: websocket` header check inside the bearer middleware let
any route skip auth by sending that header.

## Decision

- `POST /api/v1/ws-ticket` (bearer-gated) mints a random (`crypto/rand`, 32
  bytes, base64url) single-use ticket valid ~30s. `/ws?ticket=` consumes it
  (`hub.ConsumeTicket` always deletes on lookup, so replay fails). The old
  `?token=` fallback was removed and now returns 401.
- `/ws` is mounted outside `BearerAuth` in `router.go`; auth happens in
  `ws.ServeWS`. The frontend (`ws.ts`) mints a ticket first when it holds a
  token, and calls `notifyUnauthorized()` if the mint itself returns 401.
- The ticket store is a mutex-guarded map swept opportunistically on `issue()`;
  no background goroutine (single-user volume).
- `/ws` honours the same `CORS_ORIGINS` origin patterns as HTTP.
- WS auth does not resolve named actors (`API_TOKENS`); only HTTP handlers
  record actors.

## Consequences

- Never bypass auth via a request header; mount unauthenticated routes outside
  the bearer group explicitly.
- Detail: `docs/websocket.md`.
