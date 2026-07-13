# frontend/src/api

Four files that form the complete backend communication layer.

## `types.ts`

Shared TypeScript interfaces mirroring backend JSON shapes. All API functions and WS events use types from here. When backend models change, update types here first, then fix call sites.

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

## `authToken.ts`

Single source of truth for the runtime API bearer token — there's no
user/session model, just one shared token (or a named token from
`API_TOKENS`) stored in `localStorage`.

- `getApiToken()` — reads the stored token; if nothing is stored yet, seeds
  `localStorage` from the build-time `VITE_API_TOKEN` (if set) so existing
  `.env.local` dev setups keep working without a prompt, then returns that.
  This fallback only applies once — after that, whatever's in `localStorage`
  wins (including an empty value explicitly saved via the token prompt).
- `setApiToken(token)` / `clearApiToken()` — write/remove the stored token.
- `authHeaders()` — returns `{}` or `{ Authorization: 'Bearer <token>' }` to
  spread into a `fetch()` call's headers.
- `onUnauthorized(fn)` / `notifyUnauthorized()` — a tiny pub/sub used by the
  shared fetch helpers to tell `ApiTokenGate` "show the token prompt" on any
  401, without every call site needing its own error UI.

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

On `close`, reconnects after a flat 3s delay via `setTimeout(() => this.connect(), 3000)`. On `open`, re-sends all active subscriptions so no events are missed.

### Event Routing

All events are delivered to every handler registered via `on()`; callers filter by `event.type` themselves (see `TaskDetailPage`, `BoardPage`, `useRunLogs`, etc. for examples).

### Auth

If a runtime token is present (`getApiToken()` from `authToken.ts`), `connect()` first `POST`s `/api/v1/ws-ticket` (with the token as a Bearer header) to mint a short-lived, single-use ticket, then opens the socket as `?ticket=<ticket>`. This keeps the long-lived token out of the WS URL — query strings leak into reverse-proxy/access logs and browser history. `connect()` is therefore `async`; callers that fire-and-forget it (e.g. `main.tsx`) are unaffected. If the ticket fetch fails, `connect()` falls through and opens the socket without a ticket — the server rejects with 401 and the existing reconnect loop retries. If the ticket fetch itself comes back 401 (wrong/expired token), `notifyUnauthorized()` is called immediately so `ApiTokenGate` prompts for a new token even on a WS-only page.
