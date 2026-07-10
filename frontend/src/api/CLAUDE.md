# frontend/src/api

Three files that form the complete backend communication layer.

## `types.ts`

Shared TypeScript interfaces mirroring backend JSON shapes. All API functions and WS events use types from here. When backend models change, update types here first, then fix call sites.

Key types: `Task`, `AgentConfig`, `Repo`, `Workflow`, `WorkflowLabel`, `WorkflowTransition`, `AgentRun`, `AgentLog`, `DashboardStats`.

## `client.ts`

Typed fetch wrappers for every REST endpoint. Each function:
- Prepends `VITE_API_BASE_URL` (empty string in Docker = same origin)
- Sets `Authorization: Bearer` header if `VITE_API_TOKEN` is set
- Throws on non-2xx responses with the parsed error message

Function names mirror HTTP semantics: `getTasks`, `createTask`, `updateTask`, `deleteTask`, `approveTask`, `rejectTask`, `getRunLogs`, etc.

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

If `VITE_API_TOKEN` is set, `connect()` first `POST`s `/api/v1/ws-ticket` (with the token as a Bearer header) to mint a short-lived, single-use ticket, then opens the socket as `?ticket=<ticket>`. This keeps the long-lived token out of the WS URL — query strings leak into reverse-proxy/access logs and browser history. `connect()` is therefore `async`; callers that fire-and-forget it (e.g. `main.tsx`) are unaffected. If the ticket fetch fails, `connect()` falls through and opens the socket without a ticket — the server rejects with 401 and the existing reconnect loop retries.
