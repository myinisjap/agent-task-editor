# Frontend

React 19 + TypeScript + Vite + Tailwind CSS. Single-page application served by nginx in Docker.

## Stack

- **React 19** with functional components and hooks
- **TypeScript** — full `strict` (`noUncheckedIndexedAccess` intentionally off; see `tsconfig.app.json`)
- **Vite** — dev server (`:5173`) + production bundler
- **Tailwind CSS** — utility-first styling
- **Zustand** — lightweight global state
- **React Router** — client-side routing
- **@dnd-kit** — drag-and-drop on the board
- **lucide-react** (pinned) — icons; named imports only, rendered through `ui/Icon`

## Directory Layout

```
src/
├── api/
│   ├── client.ts     REST API client functions + TypeScript types
│   ├── ws.ts         WSClient class — connect, subscribe, event routing
│   ├── authToken.ts  Runtime API bearer token (localStorage) + 401 pub/sub
│   └── types.ts      Shared TypeScript type definitions
├── components/
│   ├── DependenciesPanel.tsx  Task dependency list + add/remove UI
│   ├── SubtasksPanel.tsx      Subtask checklist + add/remove UI
│   ├── board/        AgentGroupColumn, AgentLogEntry, GitStateBadge, NewTaskModal, TaskBoard, TaskCard, TaskColumn
│   ├── chat/          TerminalKeyBar (mobile Esc/Tab/arrow/Ctrl row for the chat terminal)
│   ├── diff/          FileDiffViewer (syntax-highlighted git diff + inline review comments)
│   ├── inbox/         DecisionContext (Decide row summary), InboxPreview (desktop right-pane preview)
│   ├── lessons/       LessonForm, LessonsPanel (repo/global lessons), RememberLessonModal
│   ├── ui/            Button, Badge, Card, Field, PageFrame, Icon + design tokens (see ui/README.md)
│   ├── skills/        SkillPicker (chip multi-select for agent/repo skills)
│   └── shared/        ApiTokenGate, GitHubAuthWarning, NavSidebar, Toast, WorkflowFlowchart
├── pages/
│   ├── BoardPage.tsx        Main Kanban board
│   ├── TaskDetailPage.tsx   Task detail + live agent log stream
│   ├── DashboardPage.tsx    Stats and recent activity
│   ├── WorkflowPage.tsx     Workflow editor (labels + transitions; request-sequenced loads, Save guarded against stale YAML — ADR 0034)
│   ├── AgentConfigPage.tsx  Agent config management
│   ├── ProviderConfigPage.tsx Provider config management (provider/model/env — shared by agent configs and chat sessions)
│   ├── ReposPage.tsx        Repo management
│   └── HealthPage.tsx       Provider readiness / health checks
├── stores/
│   ├── tasks.ts      Task list state + WebSocket updates (request-sequenced `fetch()`; preserves WS upserts/removes landing mid-sweep — ADR 0034)
│   ├── agents.ts     Agent config state
│   ├── providerConfigs.ts Provider config state (provider/model/env)
│   ├── repos.ts      Repo list state
│   ├── theme.ts      Dark/light theme state (persisted, syncs root class)
│   ├── notifications.ts Browser-notification opt-in + permission state (persisted; default off)
│   └── workflow.ts   Workflow + label state
├── lib/
│   ├── parseDiff.ts         Git unified diff parser
│   ├── parseAgentLog.ts     Parses raw agent log content (plain text or JSON) into a structured summary/detail view
│   ├── parseWorkflowYaml.ts Hand-rolled parser for the workflow YAML schema (labels/transitions)
│   ├── workflowEdit.ts      Pure draft-edit/validate/serialize functions behind the Pipeline graph editor
│   ├── validateWorkflow.ts  Structural validation of a parsed workflow (labels, transitions, reachability)
│   ├── condensedBoard.ts    Groups workflow labels into condensed/agent-group board columns
│   ├── diffComments.ts      Maps API review-comment wire type to the diff viewer's view model
│   ├── useIsMobile.ts       Hook for responsive breakpoint detection
│   ├── inboxKeys.ts         Inbox keyboard map (j/k, a, r, Enter/o, Esc, ?) + `useInboxKeyboard` hook; ignores typing targets and modifiers
│   ├── useMediaQuery.ts     useMediaQuery(q) / useIsWideDesktop() (≥1280px) / useIsPhone() (<768px) — pick the Task Detail review layout variant
│   ├── useKeyboardInset.ts  Hook: height of the on-screen keyboard overlapping the layout viewport
│   ├── termKeys.ts          Mobile terminal key-bar sequences + Ctrl-modifier folding
│   ├── humanGate.ts         isHumanGateLabel() — is a workflow label one only a human can move a task out of
│   ├── notify.ts            showHumanNeededNotification() — de-duped browser notification (via the service worker when present)
│   ├── serviceWorker.ts     Registers public/sw.js (needed for notifications on mobile; secure contexts only)
│   └── useHumanNeededNotifications.ts  Hook mounted at the app root: WS-driven "needs human" notifications
├── App.tsx           Router setup
└── main.tsx          Entry point
```

## Environment Variables

Set in `.env.local` for local dev (not committed):

```
VITE_API_BASE_URL=http://localhost:8080   # empty = same origin (Docker)
VITE_WS_BASE_URL=ws://localhost:8080     # empty = same origin (Docker)
VITE_API_TOKEN=                          # dev-only: seeds localStorage once if set
```

`VITE_API_TOKEN` is a dev-only convenience that seeds the runtime token in `localStorage` once. Production and the prebuilt image use the runtime flow in `src/api/authToken.ts` + `ApiTokenGate` (ADR 0032); never rely on a build-time token there.

## Development

```bash
npm install
npm run dev            # Vite dev server with HMR
npm run build          # Production build to dist/
npm run lint           # oxlint
npx tsc --noEmit       # Type-check (no dedicated script)
npm run test:coverage  # vitest run --coverage
npm run e2e            # Playwright E2E smoke tests (stack must already be running)
```

## Testing

Three layers (ADR 0035):

- **Pure-function tests** in `src/lib/*.test.ts` (no DOM).
- **Component tests** (Vitest + Testing Library, `jsdom`; setup in `vite.config.ts` and `src/test/setup.ts`) for `src/pages/*.test.tsx` and `src/components/**/*.test.tsx`. Assert on behavior, not snapshots.
- **E2E smoke tests** (`e2e/*.spec.ts`, Playwright) against the real built compose stack at `http://localhost:5173/tasks/` — nothing mocked, not `npm run dev`. Every spec runs under `chromium` and `mobile-chrome`. Needs the stack running (`./dev.sh start` or `docker compose up -d --build --wait`) and the repo directory present under `REPO_BASE_DIR` before start; `e2e/global-setup.ts` seeds the repo row. `npm run e2e:install` once. See `e2e/README.md`. Keep it small and non-flaky; it runs as its own CI job.

Mocking patterns for component tests:
- `vi.mock('../../api/client', ...)` (path relative to the test) with `vi.fn()`s; `src/test/mockApi.ts` covers the full `api` surface.
- Seed Zustand stores with `.setState(...)`, and also mock the underlying API call, since a page's mount-time `fetch()` overwrites the seed.
- Mock `wsClient` (`../../api/ws`) as `{ on: () => () => {}, subscribeTask: vi.fn(), unsubscribeTask: vi.fn() }`.
- Simulate `@dnd-kit` drags with real `fireEvent.mouseDown`/`mouseMove`/`mouseUp` plus `getBoundingClientRect` stubs (see `src/components/board/TaskBoard.test.tsx`).

CI enforces a statements coverage floor via `npm run test:coverage` (`.github/workflows/ci.yml`, "Enforce coverage floor"). `vite.config.ts` sets `coverage.all: true` with `include: ['src/**/*.{ts,tsx}']` so new untested files count as 0%; the `exclude` list is tests, generated `src/api/types.ts`, `src/main.tsx`, `src/test/**`. Don't widen the exclusions to hide gaps. CI uploads the report as the `frontend-coverage` artifact.

## Code Generation

`src/api/types.ts` is generated from the root `openapi.yaml` via
`openapi-typescript`. Do not hand-edit it. After changing `openapi.yaml`, run:

```bash
npm run gen:api
```

CI regenerates the file and fails the build (`git diff --exit-code`) if it
doesn't match what's committed, so the spec and the generated client types
can't silently diverge.

## Adding a New Page

1. Create `src/pages/MyPage.tsx`
2. Add a route in `App.tsx`
3. Add navigation link in `NavSidebar` (or, for a settings-like page, a tab in `SettingsPage`; Workflow/Agents-style pipeline config goes in `PipelinePage`)
4. Add any new API calls to `src/api/client.ts`
5. Add types via `openapi.yaml` + `npm run gen:api` (never hand-edit `src/api/types.ts`)

## Theming (dark / light) — ADR 0033

Authored **dark-first** with Tailwind `slate` plus accents. The light theme is derived by remapping `--color-<name>-<shade>` variables under a `.light` root class in `src/index.css` (regenerate with `node scripts/gen-light-theme.mjs` after a Tailwind upgrade). Prefer `slate`/accent utilities over hardcoded hex, and don't add per-component `dark:` variants. `src/stores/theme.ts` persists the choice; an inline script in `index.html` applies the class before first paint.

**Semantic tokens (ADR 0039):** new and migrated code uses the OKLCH tokens declared in `@theme` in `src/index.css` (`bg-surface`/`-raised`/`-sunken`, `text-fg`/`-muted`/`-subtle`, `border-border`, status `text-<tone>` + `bg-<tone>-soft`, `bg-accent text-on-accent`) and the `components/ui` primitives; light values live in a hand-written `.light` block separate from the generated slate remap. Use Lucide icons through `<Icon>` (aria-hidden, sized xs–lg); don't use emoji as UI icons. Token contrast is enforced by `src/theme/tokens.test.ts`; see `src/components/ui/README.md`. The slate remap and `gen-light-theme.mjs` stay for unmigrated code, and the contrast rule below applies to it.

**Contrast rule (slate/accent classes):** `white`/`black` are not remapped, and shades 50–400 / 700–900 flip while 500/600 stay put. So never put `text-white` on slate/neutral fills or on any -700/-800/-900 fill (it becomes white-on-pastel in light mode). Use `text-slate-50` (it flips) on slate fills, and keep `text-white` only on saturated -500/-600 fills. Avoid `hover:bg-slate-600` behind flipping text (600 does not flip). Never stack `text-white` with another text colour on one element (CSS order wins, not class order). `src/theme/textOnFill.test.ts` and `e2e/contrast.spec.ts` (axe `color-contrast`, both themes) enforce this.

## State Management Pattern

Zustand stores own server state. Components call store actions which call `client.ts`, then update state on success; WS events from `ws.ts` also feed the stores (no polling). Sequence fetches and merge WS updates as in ADR 0034.
