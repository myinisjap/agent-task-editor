# 30. HTTP API conventions: JSON everywhere, ordered guards, safe uploads

Status: Accepted

## Context

CORS only shapes response headers; it does not stop a cross-site request from
reaching a handler. Clients also need one error shape, and user uploads must not
become an XSS vector (#142).

## Decision

- Every `/api/v1/*` response, including middleware rejections
  (`BearerAuth` 401, `ws.ServeWS` pre-upgrade auth), is JSON `{"error": msg}`
  via `Err`/`JSON` (middleware inlines the shape rather than importing
  `handlers`, to avoid an import cycle). Only a failed `websocket.Accept`
  upgrade is plain.
- Middleware order: RequestID, Recover, Logger, `HostPolicy`, CORS,
  `RequireTrustedOrigin`, `RequireJSON`, `BearerAuth`, body limits (1 MB;
  50 MB for multipart `POST /tasks`). Request-level logs carry `request_id`;
  handlers use `middleware.LoggerFromContext`.
- `BearerAuth` compares tokens with `crypto/subtle.ConstantTimeCompare`, checks
  all named tokens without short-circuiting, then the legacy token. A named
  match becomes an actor (`ActorFromContext`) recorded in
  `task_label_history.actor_id`; the legacy token or no auth yields actor `""`.
- `RequireTrustedOrigin` does not trust `X-Forwarded-Host`.
- Uploads: the stored extension comes from the sniffed `image/*` type, never
  the client filename; `UploadsHandler` checks path components, sets
  `X-Content-Type-Options: nosniff` and `Content-Disposition: inline`, and
  overrides `Content-Type` from a trusted extension map. Images over 2000x2000
  are downscaled.
- Auto-clone (`POST /repos` with only `remote_url`) is asynchronous: 201 with
  `clone_status: cloning`, clone in a goroutine under a detached 30-minute
  context (not the request context, because of the server's 60s
  `WriteTimeout`), then `repo.clone_done`/`repo.clone_failed`.

## Consequences

- New non-JSON write routes must be added to `jsonExemptions`.
- Tests inject `RunCanceller`/`ReplyDispatcher`/`RepoEventPublisher` fakes; the
  handlers are nil-safe.
