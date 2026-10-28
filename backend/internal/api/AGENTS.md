# internal/api

Chi v5 router wired in `router.go`. All HTTP handlers and middleware live under this package.

## Router Structure

```
GET  /healthz               (no auth; static liveness stub)
GET  /readyz                (no auth; DB ping + dispatcher heartbeat — what the compose healthcheck targets)
GET  /ws                    (auth via single-use ?ticket=, see POST /ws-ticket below; the old ?token= fallback was removed and now 401s)
GET  /metrics                (Prometheus scrape; gated by METRICS_TOKEN, else by the API tokens when API auth is configured unless METRICS_PUBLIC=true; outside /api/v1 and outside the main bearer group)
POST /api/v1/intake/{endpoint_id}  IntakeEndpointsHandler.Receive (outside BearerAuth; HMAC X-ATE-Signature-256 is the auth, own 256 KB cap + per-endpoint rate limit; also POST /api/v1/github/webhook likewise)
  /intake-endpoints[/{id}[/rotate-secret|/test]]  IntakeEndpointsHandler CRUD (inside /api/v1, bearer-gated)
/api/v1/
  POST body limit: 1 MB (50 MB for POST /tasks, which handles image uploads via multipart)
  POST /ws-ticket             WSTicketHandler (mints single-use WS auth ticket; bearer-gated)
  GET  /backup                 BackupHandler (point-in-time DB snapshot; see docs/backup.md)
  tasks/*                     TasksHandler
  tasks/{id}/runs/*            TasksHandler (list/get/logs/cancel/reply)
  tasks/{id}/label-history     TasksHandler.ListLabelHistory (task_label_history audit trail)
  tasks/{id}/dependencies/*    DependenciesHandler (dispatch-gating peer task dependencies)
  tasks/{id}/subtasks          SubtasksHandler (create_subtask MCP tool)
  tasks/{id}/review-comments/* ReviewCommentsHandler (inline diff review comments)
  tasks/{id}/review-brief      ReviewBriefHandler (deterministic risk flags + verification summary; internal/reviewbrief)
  tasks/{id}/autonomy-decision AutonomyHandler (latest shadow autonomy decision for the current label)
  tasks/{id}/always-ask        TasksHandler.SetAlwaysAsk (per-task autonomy override)
  templates/*                  TemplatesHandler
  lessons/*, repos/{id}/lessons LessonsHandler
  uploads/{task_id}/{filename} UploadsHandler (serve attachment images)
  workflows/*                  WorkflowsHandler
  agents/*                     AgentsHandler
  repos/*                      ReposHandler
  dashboard/*                  DashboardHandler
  github/auth-status           GitHub CLI auth status
  health/providers             HealthHandler (provider/onboarding readiness checks)
```

## Middleware Chain (in order; ADR 0030)

1. `chimiddleware.RequestID` — must be first so every layer sees the id; sets `X-Request-Id`
2. `middleware.Recover` — panics → logged stack + `request_id`, 500
3. `middleware.Logger` — slog request log (method, path, status, duration, `request_id`)
4. `HostPolicy.Middleware` — DNS-rebinding Host allowlist; enforced only when no API token is set (`ALLOWED_HOSTS`); applies to every route incl. `/ws`, `/healthz`, `/metrics`
5. `middleware.CORS` — from `CORS_ORIGINS`
6. `middleware.RequireTrustedOrigin` — 403 for POST/PUT/PATCH/DELETE whose `Origin` is neither in `CORS_ORIGINS` nor same-origin (no Origin = pass)
7. `middleware.RequireJSON` — 415 for non-JSON POST/PUT/PATCH bodies; exempt: multipart `POST /api/v1/tasks`, YAML workflow import/update. **Add new non-JSON routes to `jsonExemptions`.**
8. `middleware.BearerAuth` — legacy `bearerToken` and/or `namedTokens`; a named match is exposed via `middleware.ActorFromContext` and passed as `actorID` to `engine.Transition` by human-triggered handlers. No-op when both empty.
9. Per-route 1 MB body limit on `/api/v1/*`

`/ws` is mounted **outside** `BearerAuth` and authenticated in `ws.ServeWS` via single-use tickets (ADR 0022). Never skip auth via a request header.

### Logging

Request-level logs include `request_id`. Handlers use `middleware.LoggerFromContext(r.Context())` for a logger pre-scoped with it.

## Handler Conventions

- Handlers are structs with constructor `NewXxxHandler(q *gen.Queries, ...)`
- JSON decode errors → `400`; not found → `404`; validation → `400` with `{"error":"message"}`
- Always respond through `Err(w, status, msg)` / `JSON(w, status, v)` so every `/api/v1/*` response is JSON

See `handlers/AGENTS.md` for per-handler details.
