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
ws.connect()                        // open connection, start reconnect loop
ws.subscribe(taskId: string)        // send subscribe message
ws.unsubscribe(taskId: string)      // send unsubscribe message
ws.onLog(taskId, handler)           // register agent.log listener for a task
ws.onEvent(type, handler)           // register listener for non-log events
ws.offLog(taskId)                   // remove log listener
ws.offEvent(type)                   // remove event listener
```

### Reconnect Behaviour

Uses exponential back-off: 1s, 2s, 4s, 8s, … up to 30s. On reconnect, re-sends all active subscriptions so no events are missed. Reconnect is automatic unless `ws.close()` is called explicitly.

### Event Routing

- `agent.log` events are matched by `payload.task_id` and delivered only to the matching `onLog` handler
- All other events (`task.label_changed`, `task.agent_started`, `task.agent_done`, `task.needs_human`) are delivered to `onEvent` handlers registered for that type

### Token

Appended as `?token=<VITE_API_TOKEN>` on the WebSocket URL if the env var is set.
