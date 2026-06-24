# Frontend

React 18 + TypeScript + Vite + Tailwind CSS. Single-page application served by nginx in Docker.

## Stack

- **React 18** with functional components and hooks
- **TypeScript** — strict mode enabled
- **Vite** — dev server (`:5173`) + production bundler
- **Tailwind CSS** — utility-first styling
- **Zustand** — lightweight global state
- **React Router** — client-side routing
- **@dnd-kit** — drag-and-drop on the board

## Directory Layout

```
src/
├── api/
│   ├── client.ts     REST API client functions + TypeScript types
│   ├── ws.ts         WSClient class — connect, subscribe, event routing
│   └── types.ts      Shared TypeScript type definitions
├── components/
│   ├── board/        KanbanBoard, KanbanColumn, TaskCard, NewTaskModal
│   ├── diff/         DiffViewer (syntax-highlighted git diff)
│   └── shared/       Reusable UI primitives (Button, Badge, Modal, etc.)
├── pages/
│   ├── BoardPage.tsx        Main Kanban board
│   ├── TaskDetailPage.tsx   Task detail + live agent log stream
│   ├── DashboardPage.tsx    Stats and recent activity
│   ├── WorkflowPage.tsx     Workflow editor (labels + transitions)
│   └── AgentConfigPage.tsx  Agent config management
├── stores/
│   ├── tasks.ts      Task list state + WebSocket updates
│   ├── agents.ts     Agent config state
│   └── workflow.ts   Workflow + label state
├── lib/
│   └── parseDiff.ts  Git unified diff parser
├── App.tsx           Router setup
└── main.tsx          Entry point
```

## Environment Variables

Set in `.env.local` for local dev (not committed):

```
VITE_API_BASE_URL=http://localhost:8080   # empty = same origin (Docker)
VITE_WS_BASE_URL=ws://localhost:8080     # empty = same origin (Docker)
VITE_API_TOKEN=                          # bearer token if API_TOKEN is set
```

## Development

```bash
npm install
npm run dev       # Vite dev server with HMR
npm run build     # Production build to dist/
npm run lint      # ESLint
npm run type-check  # tsc --noEmit
```

## Adding a New Page

1. Create `src/pages/MyPage.tsx`
2. Add a route in `App.tsx`
3. Add navigation link in the layout component
4. Add any new API calls to `src/api/client.ts`
5. Add types to `src/api/types.ts`

## State Management Pattern

Zustand stores own server state. Components call store actions which call `client.ts` functions, then update store state on success. WebSocket events from `ws.ts` are also wired into stores to keep state live without polling.
