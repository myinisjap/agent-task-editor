# internal/api

Chi v5 router wired in `router.go`. All HTTP handlers and middleware live under this package.

## Router Structure

```
GET  /healthz              (no auth)
GET  /ws                   (auth via ?token= query param)
/api/v1/
  POST body limit: 1 MB
  tasks/*                  TasksHandler
  workflows/*              WorkflowsHandler
  agents/*                 AgentsHandler
  repos/*                  ReposHandler
  dashboard                DashboardHandler
```

## Middleware Chain (in order)

1. `middleware.Recover` — catches panics, returns 500
2. `middleware.Logger` — structured slog request logging
3. `chimiddleware.RequestID` — injects `X-Request-Id`
4. `middleware.CORS` — sets CORS headers; configured from `CORS_ORIGINS`
5. `middleware.BearerAuth` — validates `Authorization: Bearer <token>` (skips if token empty)
6. Per-route 1 MB body limit on `/api/v1/*`

## Handler Conventions

- Handlers are structs with constructor `NewXxxHandler(q *gen.Queries, ...)` 
- JSON decode errors → `400 Bad Request`
- Not found → `404`
- Validation errors → `400` with `{"error":"message"}`
- `Err(w, status, msg)` helper writes JSON error response
- `JSON(w, status, v)` helper marshals response

See `handlers/CLAUDE.md` for per-handler details.
