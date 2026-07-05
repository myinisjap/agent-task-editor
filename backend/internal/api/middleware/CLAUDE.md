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

If `API_TOKEN` is set, requires `Authorization: Bearer <token>` on every request. Uses `crypto/subtle.ConstantTimeCompare` to prevent timing attacks. Skips auth entirely when token is empty (development mode).

The `/ws` route is mounted *outside* this middleware (see `router.go`) rather than bypassed via a request header — an earlier `Upgrade: websocket` header check let any route skip auth. WebSocket auth is handled by `ws.ServeWS` via the `?token=` query param, since browsers can't set request headers on a WS handshake.
