# frontend/src/api

The backend communication layer: `types.ts`, `client.ts`, `authToken.ts`, `ws.ts`.

## `types.ts`

Generated from the root `openapi.yaml` (`npm run gen:api`) — **do not hand-edit**; CI fails on drift. When backend models change, edit `openapi.yaml`, regenerate, then fix call sites.

Key types: `Task`, `AgentConfig`, `Repo`, `Workflow`, `WorkflowLabel`, `WorkflowTransition`, `AgentRun`, `AgentLog`, `DashboardStats`.

## `client.ts`

Typed fetch wrappers for every REST endpoint. Each function:
- Prepends `BASE` (`import.meta.env.BASE_URL` + `/api/v1`)
- Sets `Authorization: Bearer` header from a runtime token in `localStorage`
  (`ate_api_token`, managed by `authToken.ts`) when present — every request
  goes through `authedRawFetch` (used directly by `agents.create` and
  exported for other modules' raw `fetch()` calls) or the internal
  `request()`/`requestWithHeaders()` wrappers, which call it too. On a 401
  response, the stored token is cleared and `ApiTokenGate`
  (`components/shared/ApiTokenGate.tsx`) is notified so it can prompt for a
  new one.
- Throws on non-2xx responses with the parsed error message

Function names mirror HTTP semantics: `getTasks`, `createTask`, `updateTask`, `deleteTask`, `approveTask`, `rejectTask`, `getRunLogs`, etc.

## `authToken.ts` (ADR 0032)

Single source of truth for the runtime API bearer token (one shared or named token in `localStorage`, key `ate_api_token`; no session model).

- `getApiToken()` reads it, seeding once from build-time `VITE_API_TOKEN` if nothing is stored; after that `localStorage` wins, including an empty value saved via the prompt.
- `setApiToken(token)` / `clearApiToken()` write/remove it.
- `authHeaders()` returns `{}` or `{ Authorization: 'Bearer <token>' }`; use it for any new raw `fetch()`.
- `onUnauthorized(fn)` / `notifyUnauthorized()` — tiny pub/sub telling `ApiTokenGate` to show the prompt on any 401.

## `ws.ts`

`WSClient` — singleton-style class managing one WebSocket connection.

### Key Methods

```ts
wsClient.connect()                   // async — open connection (fetches a ticket first; see Auth below)
wsClient.on(handler)                 // register a handler for every WSEvent; returns an unsubscribe fn
wsClient.subscribeTask(taskId)       // send subscribe message
wsClient.unsubscribeTask(taskId)     // send unsubscribe message
```

### Reconnect Behaviour

On `close`, reconnect with capped exponential backoff and full jitter (`min(30s, 1s * 2^attempts)`, jittered to `[delay/2, delay]`; `attempts` resets only on a confirmed `open`) so tabs don't hammer the server in lockstep. On `open`, re-send all active subscriptions. `getStatus()` / `onStatusChange(fn)` expose `'connecting' | 'open' | 'closed'` (see `lib/useWsStatus.ts`, `NavSidebar`). Handle the server's `resync` frame by refetching state (ADR 0023).

### Event Routing

All events are delivered to every handler registered via `on()`; callers filter by `event.type` themselves (see `TaskDetailPage`, `BoardPage`, `useRunLogs`, etc. for examples).

### Auth (ADR 0022)

With a runtime token, `connect()` first `POST`s `/api/v1/ws-ticket` (Bearer) to mint a short-lived single-use ticket and opens the socket as `?ticket=<ticket>`, keeping the long-lived token out of the URL. `connect()` is async; fire-and-forget callers are fine. If the mint fails it falls through and the server's 401 plus the reconnect loop handle it; if the mint itself returns 401, `notifyUnauthorized()` fires so `ApiTokenGate` prompts even on a WS-only page.
