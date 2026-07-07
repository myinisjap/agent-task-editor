# internal/api

Chi v5 router wired in `router.go`. All HTTP handlers and middleware live under this package.

## Router Structure

```
GET  /healthz              (no auth)
GET  /ws                   (auth via ?token= query param)
/api/v1/
  POST body limit: 1 MB
  tasks/*                  TasksHandler
  tasks/{id}/dependencies/* DependenciesHandler (dispatch-gating peer task dependencies)
  tasks/{id}/subtasks       SubtasksHandler (create_subtask MCP tool)
  tasks/{id}/review-comments/* ReviewCommentsHandler (inline diff review comments)
  templates/*               TemplatesHandler
  uploads/{task_id}/{filename} UploadsHandler (serve attachment images)
  workflows/*               WorkflowsHandler
  agents/*                  AgentsHandler
  repos/*                   ReposHandler
  dashboard                 DashboardHandler
  github/auth-status         GitHub CLI auth status
  health/providers           HealthHandler (provider/onboarding readiness checks)
```

## Middleware Chain (in order)

1. `chimiddleware.RequestID` — generates a request ID and injects it into the
   request context (must run first so every downstream middleware/handler can
   read it) and sets the `X-Request-Id` response header
2. `middleware.Recover` — catches panics, logs the stack trace + `request_id`, returns 500
3. `middleware.Logger` — structured slog request logging (method, path, status,
   duration, `request_id`)
4. `middleware.CORS` — sets CORS headers; configured from `CORS_ORIGINS`
5. `middleware.BearerAuth` — validates `Authorization: Bearer <token>` (skips if token empty)
6. Per-route 1 MB body limit on `/api/v1/*`

### Logging conventions

- All request-level logs (`middleware.Logger`, `middleware.Recover`) include a
  `request_id` field (from `chimiddleware.GetReqID`), so a single request can be
  traced across log lines.
- Handlers that need to log something tied to the in-flight request (instead of
  using the bare `slog` package functions) should call
  `middleware.LoggerFromContext(r.Context())` to get a `*slog.Logger` pre-scoped
  with `request_id`, then use `logger.Warn(...)`/`logger.Error(...)` etc.

## Handler Conventions

- Handlers are structs with constructor `NewXxxHandler(q *gen.Queries, ...)` 
- JSON decode errors → `400 Bad Request`
- Not found → `404`
- Validation errors → `400` with `{"error":"message"}`
- `Err(w, status, msg)` helper writes JSON error response
- `JSON(w, status, v)` helper marshals response

See `handlers/CLAUDE.md` for per-handler details.
