# Frontend

React 19 + TypeScript + Vite + Tailwind CSS. Single-page application served by nginx in Docker.

## Stack

- **React 19** with functional components and hooks
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
│   ├── DependenciesPanel.tsx  Task dependency list + add/remove UI
│   ├── SubtasksPanel.tsx      Subtask checklist + add/remove UI
│   ├── board/        AgentGroupColumn, AgentLogEntry, GitStateBadge, NewTaskModal, TaskBoard, TaskCard, TaskColumn
│   ├── diff/          FileDiffViewer (syntax-highlighted git diff + inline review comments)
│   └── shared/        GitHubAuthWarning, NavSidebar, WorkflowFlowchart
├── pages/
│   ├── BoardPage.tsx        Main Kanban board
│   ├── TaskDetailPage.tsx   Task detail + live agent log stream
│   ├── DashboardPage.tsx    Stats and recent activity
│   ├── WorkflowPage.tsx     Workflow editor (labels + transitions)
│   ├── AgentConfigPage.tsx  Agent config management
│   ├── ReposPage.tsx        Repo management
│   └── HealthPage.tsx       Provider readiness / health checks
├── stores/
│   ├── tasks.ts      Task list state + WebSocket updates
│   ├── agents.ts     Agent config state
│   ├── repos.ts      Repo list state
│   ├── theme.ts      Dark/light theme state (persisted, syncs root class)
│   └── workflow.ts   Workflow + label state
├── lib/
│   ├── parseDiff.ts         Git unified diff parser
│   ├── parseAgentLog.ts     Parses raw agent log content (plain text or JSON) into a structured summary/detail view
│   ├── parseWorkflowYaml.ts Hand-rolled parser for the workflow YAML schema (labels/transitions)
│   ├── validateWorkflow.ts  Structural validation of a parsed workflow (labels, transitions, reachability)
│   ├── condensedBoard.ts    Groups workflow labels into condensed/agent-group board columns
│   ├── diffComments.ts      Maps API review-comment wire type to the diff viewer's view model
│   └── useIsMobile.ts       Hook for responsive breakpoint detection
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
npm run dev            # Vite dev server with HMR
npm run build          # Production build to dist/
npm run lint           # oxlint
npx tsc --noEmit       # Type-check (no dedicated script)
npm run test:coverage  # vitest run --coverage
```

## Code Generation

`src/api/types.ts` is generated from the root `openapi.yaml` via
`openapi-typescript`. Do not hand-edit it. After changing `openapi.yaml`, run:

```bash
npm run gen:api
```

CI regenerates the file and fails the build (`git diff --exit-code`) if it
doesn't match what's committed, so the spec and the generated client types
can't silently diverge.

CI also uploads a `vitest run --coverage` report as a build artifact
(`frontend-coverage`) on every run so coverage trends stay visible on PRs.

## Adding a New Page

1. Create `src/pages/MyPage.tsx`
2. Add a route in `App.tsx`
3. Add navigation link in the layout component
4. Add any new API calls to `src/api/client.ts`
5. Add types to `src/api/types.ts`

## Theming (dark / light)

The UI is authored **dark-first** with Tailwind's `slate` palette (plus accent colors).
Rather than add `dark:` variants everywhere, the dark palette is kept as-is and a **light**
theme is derived by remapping Tailwind's `--color-<name>-<shade>` CSS variables under a
`.light` root class in `src/index.css`. Tailwind v4 compiles utilities to
`var(--color-...)`, so overriding those variables re-themes existing classes with no
component edits. The light values are a perceptual index-mirror of the dark ramp — regenerate
them with `node scripts/gen-light-theme.mjs` after a Tailwind upgrade.

`src/stores/theme.ts` owns the `'dark' | 'light'` state (persisted to `localStorage`,
defaulting to `prefers-color-scheme`) and toggles the root class; an inline script in
`index.html` applies the class before first paint to avoid a flash. Because the theme rides
on CSS variables, prefer Tailwind `slate`/accent utilities over hardcoded hex so new UI
themes automatically.

## State Management Pattern

Zustand stores own server state. Components call store actions which call `client.ts` functions, then update store state on success. WebSocket events from `ws.ts` are also wired into stores to keep state live without polling.
