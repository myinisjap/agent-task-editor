# internal/api/middleware

Four middleware functions used by the Chi router.

## `Recover`

Wraps each request in a deferred recover. On panic, logs the stack trace and returns `500 Internal Server Error`. Prevents a single bad handler from crashing the server.

## `Logger`

Structured request logging via `log/slog`. Logs method, path, status code, and duration on each request completion.

## `CORS`

Sets `Access-Control-Allow-Origin`, `Access-Control-Allow-Methods`, and `Access-Control-Allow-Headers`. Handles `OPTIONS` preflight requests with `204`.

Configured from the `CORS_ORIGINS` env var:
- `*` or empty → allow all origins
- Comma-separated list → set the header to the request's `Origin` if it matches, otherwise omit

## `BearerAuth`

`BearerAuth(bearerToken string, namedTokens map[string]string)` requires `Authorization: Bearer <token>` on every request when either `bearerToken` (the legacy single shared token, from `API_TOKEN`) or `namedTokens` (from `API_TOKENS`, a `name -> token` map) is non-empty. Skips auth entirely when both are empty (development mode). Uses `crypto/subtle.ConstantTimeCompare` to prevent timing attacks; a request's token is checked against every `namedTokens` candidate (without short-circuiting on the first match) and then, if unmatched, against the legacy `bearerToken`.

When a request's token matches an entry in `namedTokens`, that name is stored in the request context and exposed via `ActorFromContext(ctx) string`. Handlers use this to record *who* performed a human-triggered transition in `task_label_history.actor_id` (see `handlers/tasks.go`). A match against the legacy `bearerToken` (or no auth configured at all) resolves to actor `""`, matching prior anonymous behavior — this keeps existing deployments that only set `API_TOKEN` working unchanged.

Note: the `/ws` WebSocket route does not resolve named actors — it only supports the single legacy `bearerToken` via its `?token=` query param check (see below), since browsers can't set request headers on a WS handshake and `ws.ServeWS`'s signature wasn't extended in this pass.

The `/ws` route is mounted *outside* this middleware (see `router.go`) rather than bypassed via a request header — an earlier `Upgrade: websocket` header check let any route skip auth. WebSocket auth is handled by `ws.ServeWS` via the `?token=` query param, since browsers can't set request headers on a WS handshake.
