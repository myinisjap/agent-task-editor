# internal/api/middleware

Middleware used by the Chi router (order and rationale: `../AGENTS.md`, ADR 0030).

- `Recover` — deferred recover; logs stack and returns 500.
- `Logger` — slog request log: method, path, status, duration, `request_id`.
- `CORS` — sets Allow-Origin/Methods/Headers and answers `OPTIONS` with 204. `CORS_ORIGINS`: `*` or empty = allow all; a comma list echoes the request `Origin` only if it matches.

## Request guards (`requestguard.go`)

CORS only shapes response headers; these stop the request.

- `RequireTrustedOrigin(corsOrigins)`: 403 on POST/PUT/PATCH/DELETE when `Origin` is neither listed nor same-origin (host:port == `r.Host`, default ports normalised). **`X-Forwarded-Host` is deliberately not trusted.** No `Origin`, or a safe method (incl. preflight), passes; `*` is permissive.
- `RequireJSON`: 415 on a non-empty POST/PUT/PATCH body that isn't `application/json` (charset allowed). Exemptions live in `jsonExemptions`.
- `NewHostPolicy(allowedHosts, corsOrigins, authEnabled).Middleware()`: Host allowlist enforced only without any API token. Built-ins: localhost, 127.0.0.1, ::1, backend, IP literals, CORS origin hostnames, plus `ALLOWED_HOSTS` (`*` disables).

## `BearerAuth(bearerToken, namedTokens)`

Requires `Authorization: Bearer <token>` when either is non-empty; skips auth when both are empty (development mode). Use `crypto/subtle.ConstantTimeCompare`; check every named token without short-circuiting, then the legacy token. A named match is stored in the request context and read with `ActorFromContext(ctx)` (recorded in `task_label_history.actor_id`); the legacy token or no auth yields `""`. The 401 inlines `{"error":"unauthorized"}` instead of importing `handlers` (import cycle).

`/ws` is not behind this middleware and does not resolve named actors (ADR 0022).
