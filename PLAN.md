# Agentic Code Editor — Implementation Plan

## Vision

A client-server application for managing AI-driven software development tasks. Work items
are represented as cards with labels that drive a configurable state machine. Agent processes
automatically pick up tasks matching their assigned labels, do the work, and advance the label
when done. Humans review at defined gates. The full agent log, file diffs, and approval controls
are available by drilling into any task card.

---

## Stack

| Layer | Technology |
|---|---|
| Backend | Go 1.23 + Chi v5 |
| WebSockets | `nhooyr.io/websocket` |
| Database | SQLite via `mattn/go-sqlite3` |
| DB queries | `sqlc` (type-safe codegen) |
| DB migrations | `golang-migrate` |
| Agent runtime (Claude) | `claude -p` CLI subprocess + stream-json |
| Agent runtime (other) | Raw HTTP tool-use loop (generic adapter) |
| Agent signaling | MCP sidecar server (per-run, stdio) |
| Frontend | React 19 + TypeScript + Vite |
| Styling | Tailwind CSS v4 |
| State management | Zustand |
| Diff viewer | `react-diff-viewer-continued` |
| Workflow graph editor | React Flow |
| Task runner | `xc` (reads from `TASKS.md`) |
| Go hot reload | `air` |
| API contract | OpenAPI 3.1 spec → `openapi-typescript` codegen |

---

## Default Workflow

```
[not_ready] ──human──► [plan] ──agent──► (human gate) ──► [todo]
                                                                │
                                                             agent
                                                                │
                                                                ▼
[done] ◄──human──  [review] ◄──agent──  [agent-review] ◄──agent── [testing]
                       │                       │
                    human                    agent
                       │                       │
                       └──────────────────────►┘
                              [in-progress] ◄──agent── (tests fail)
```

| Label | Color | Agent Ignore | Terminal | Default Trigger |
|---|---|---|---|---|
| `not_ready` | gray | ✓ | | — agents never pick up |
| `plan` | purple | | | agent drafts plan → human approves → `todo` |
| `todo` | blue | | | agent picks up → `in-progress` |
| `in-progress` | yellow | | | agent finishes → `testing` |
| `testing` | orange | | | agent runs tests → `agent-review` or back to `in-progress` |
| `agent-review` | indigo | | | review agent → `review` or back to `in-progress` |
| `review` | pink | | | human approves → `done` or rejects → `in-progress` |
| `done` | green | | ✓ | — terminal state |

Feedback loops:
- `testing` → `in-progress` (agent trigger, tests fail)
- `agent-review` → `in-progress` (agent trigger, issues found, with feedback)
- `review` → `in-progress` (human trigger, with rejection note)
- any label → `not_ready` (human only, to park a task)

---

## Repository Structure

```
agent-task-editor/
├── PLAN.md
├── TASKS.md                         # xc task runner definitions
├── openapi.yaml                     # API contract (source of truth)
├── docker-compose.yml
├── backend/
│   ├── cmd/
│   │   ├── server/
│   │   │   └── main.go              # HTTP server entrypoint
│   │   └── mcp-server/
│   │       └── main.go              # Per-run MCP sidecar entrypoint
│   ├── internal/
│   │   ├── api/
│   │   │   ├── router.go
│   │   │   ├── middleware/
│   │   │   │   ├── cors.go
│   │   │   │   ├── logger.go
│   │   │   │   └── recover.go
│   │   │   └── handlers/
│   │   │       ├── tasks.go
│   │   │       ├── workflows.go
│   │   │       ├── agents.go
│   │   │       ├── repos.go
│   │   │       ├── dashboard.go
│   │   │       └── ws.go
│   │   ├── agent/
│   │   │   ├── provider.go          # AgentProvider interface + shared types
│   │   │   ├── claude.go            # Claude Code CLI runner (-p + stream-json)
│   │   │   ├── llm.go               # Generic HTTP tool-use loop runner
│   │   │   ├── mcp.go               # MCP sidecar config generation
│   │   │   ├── dispatcher.go        # Sweep loop: finds tasks, dispatches to pool
│   │   │   └── pool.go              # Bounded goroutine worker pool
│   │   ├── workflow/
│   │   │   ├── engine.go            # Transition validation, state machine
│   │   │   └── gates.go             # Human gate enforcement
│   │   ├── storage/
│   │   │   ├── db.go                # Connection + migration runner
│   │   │   ├── seed.go              # Default workflow seed on first run
│   │   │   ├── migrations/
│   │   │   │   └── 001_init.sql
│   │   │   ├── queries/             # Hand-written SQL files (sqlc input)
│   │   │   │   ├── tasks.sql
│   │   │   │   ├── workflows.sql
│   │   │   │   ├── agents.sql
│   │   │   │   ├── repos.sql
│   │   │   │   └── runs.sql
│   │   │   └── gen/                 # sqlc-generated Go (do not edit)
│   │   ├── git/
│   │   │   ├── repo.go              # Repo registration + validation
│   │   │   └── diff.go              # git diff / git ls-tree wrappers
│   │   └── ws/
│   │       ├── hub.go               # Fan-out broadcast hub
│   │       └── client.go            # Per-connection read/write pumps
│   ├── go.mod
│   └── sqlc.yaml
└── frontend/
    ├── src/
    │   ├── api/
    │   │   ├── client.ts            # Generated from openapi.yaml
    │   │   └── ws.ts                # Typed WebSocket client
    │   ├── stores/
    │   │   ├── tasks.ts
    │   │   ├── workflow.ts
    │   │   └── agents.ts
    │   ├── components/
    │   │   ├── board/
    │   │   │   ├── TaskBoard.tsx    # Kanban columns
    │   │   │   ├── TaskColumn.tsx
    │   │   │   └── TaskCard.tsx
    │   │   ├── task/
    │   │   │   ├── TaskDetail.tsx   # Three-panel layout
    │   │   │   ├── AgentLogStream.tsx
    │   │   │   ├── FileDiffViewer.tsx
    │   │   │   └── ApprovalPanel.tsx
    │   │   ├── dashboard/
    │   │   │   ├── Dashboard.tsx
    │   │   │   ├── StateCounts.tsx
    │   │   │   ├── ActiveAgents.tsx
    │   │   │   └── InterventionQueue.tsx
    │   │   └── workflow/
    │   │       ├── WorkflowEditor.tsx
    │   │       └── TransitionEdge.tsx
    │   ├── pages/
    │   │   ├── BoardPage.tsx
    │   │   ├── DashboardPage.tsx
    │   │   ├── TaskDetailPage.tsx
    │   │   ├── WorkflowPage.tsx
    │   │   └── AgentConfigPage.tsx
    │   └── App.tsx
    ├── package.json
    └── vite.config.ts
```

---

## Database Schema

```sql
-- 001_init.sql

CREATE TABLE workflows (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE workflow_labels (
    id           TEXT PRIMARY KEY,
    workflow_id  TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    color        TEXT NOT NULL DEFAULT '#6B7280',
    sort_order   INTEGER NOT NULL DEFAULT 0,
    agent_ignore BOOLEAN NOT NULL DEFAULT FALSE,
    is_terminal  BOOLEAN NOT NULL DEFAULT FALSE,
    UNIQUE(workflow_id, name)
);

CREATE TABLE workflow_transitions (
    id              TEXT PRIMARY KEY,
    workflow_id     TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    from_label      TEXT NOT NULL,
    to_label        TEXT NOT NULL,
    trigger_type    TEXT NOT NULL CHECK (trigger_type IN ('agent', 'human', 'both')),
    agent_config_id TEXT REFERENCES agent_configs(id) ON DELETE SET NULL,
    UNIQUE(workflow_id, from_label, to_label)
);

CREATE TABLE agent_configs (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    provider      TEXT NOT NULL,          -- claude_code | openai | anthropic | custom
    model         TEXT NOT NULL,
    system_prompt TEXT NOT NULL DEFAULT '',
    labels        TEXT NOT NULL DEFAULT '[]',  -- JSON array of label names
    env           TEXT NOT NULL DEFAULT '{}',  -- JSON object of extra env vars
    max_tokens    INTEGER NOT NULL DEFAULT 8192,
    timeout_secs  INTEGER NOT NULL DEFAULT 600,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE repos (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL UNIQUE,     -- absolute local filesystem path
    remote_url  TEXT,
    workflow_id TEXT REFERENCES workflows(id) ON DELETE SET NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE tasks (
    id                   TEXT PRIMARY KEY,
    title                TEXT NOT NULL,
    description          TEXT NOT NULL DEFAULT '',
    type                 TEXT NOT NULL DEFAULT 'feature', -- feature|bug|chore|spike
    label                TEXT NOT NULL DEFAULT 'not_ready',
    repo_id              TEXT NOT NULL REFERENCES repos(id),
    workflow_id          TEXT NOT NULL REFERENCES workflows(id),
    current_agent_run_id TEXT,
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE task_label_history (
    id         TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    from_label TEXT,
    to_label   TEXT NOT NULL,
    trigger    TEXT NOT NULL,   -- agent | human
    actor_id   TEXT,            -- agent_run_id or future user id
    note       TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE agent_runs (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_config_id TEXT NOT NULL REFERENCES agent_configs(id),
    status          TEXT NOT NULL DEFAULT 'pending',
    -- pending | running | completed | failed | waiting_human | cancelled
    feedback        TEXT,        -- human rejection note injected into next run
    started_at      DATETIME,
    completed_at    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE agent_logs (
    id           TEXT PRIMARY KEY,
    agent_run_id TEXT NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    timestamp    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    type         TEXT NOT NULL,  -- stdout | stderr | system | tool_call | tool_result
    content      TEXT NOT NULL
);

CREATE INDEX idx_tasks_label     ON tasks(label);
CREATE INDEX idx_tasks_repo      ON tasks(repo_id);
CREATE INDEX idx_agent_runs_task ON agent_runs(task_id);
CREATE INDEX idx_agent_logs_run  ON agent_logs(agent_run_id);
CREATE INDEX idx_history_task    ON task_label_history(task_id);
```

---

## Backend — Core Interfaces

### `internal/agent/provider.go`

```go
type LogEntry struct {
    Type    string    // stdout | stderr | system | tool_call | tool_result
    Content string
    At      time.Time
}

type Result struct {
    Status    string  // completed | failed | waiting_human
    NextLabel *string // agent-requested transition (validated by workflow engine)
    Message   *string // summary or human help request
}

type RunInput struct {
    Task        storage.Task
    AgentConfig storage.AgentConfig
    RepoPath    string
    Feedback    *string // prior human rejection note, if any
    PriorPlan   *string // output of the plan stage, passed into later stages
}

type Provider interface {
    Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error)
}
```

### `internal/agent/claude.go` — Claude Code CLI Runner

Spawns `claude -p` as a subprocess. Streams stdout/stderr via two goroutines into `logCh`.
Parses stream-json NDJSON lines, forwarding each as a typed `LogEntry`. The agent signals
next state via an MCP tool call (captured from the `tool_use` stream-json events).

```go
type ClaudeRunner struct {
    BinaryPath string
    MCPManager *MCPManager
}

func (r *ClaudeRunner) Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error) {
    mcpConfig, cleanup, err := r.MCPManager.StartSidecar(input.AgentRun.ID)
    defer cleanup()

    args := []string{
        "-p", buildUserPrompt(input),
        "--system", buildSystemPrompt(input),
        "--output-format", "stream-json",
        "--allowedTools", "Edit,Write,Read,Bash,Glob,Grep,task-editor__signal_complete,task-editor__request_human",
        "--mcp-config", mcpConfig,
        "--max-turns", "50",
    }

    cmd := exec.CommandContext(ctx, r.BinaryPath, args...)
    cmd.Dir = input.RepoPath
    cmd.Env = mergeEnv(os.Environ(), input.AgentConfig.Env)

    // Two goroutines stream stdout/stderr into logCh.
    // stdout is also parsed for stream-json signal events.
    // Result is extracted from the MCP tool call captured by the sidecar.
}
```

### `internal/agent/mcp.go` — MCP Sidecar

Started once per agent run. Exposes two tools over stdio MCP protocol:

**`signal_complete`**
```json
{
  "name": "signal_complete",
  "description": "Call this when your work is done to advance the task to the next workflow stage.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "next_label": { "type": "string", "description": "The label to move the task to." },
      "summary":    { "type": "string", "description": "Brief summary of what was done." }
    },
    "required": ["next_label", "summary"]
  }
}
```

**`request_human`**
```json
{
  "name": "request_human",
  "description": "Pause and ask a human for input or approval before continuing.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "message": { "type": "string", "description": "What you need from the human." }
    },
    "required": ["message"]
  }
}
```

The sidecar writes the result to a channel that `ClaudeRunner.Run` reads on process exit.

### `internal/agent/llm.go` — Generic HTTP Runner

Tool-use loop for OpenAI-compatible APIs. Runs until the model calls `signal_complete` or
`request_human`, or until `max_turns` is reached. Built-in tools:

| Tool | Description |
|---|---|
| `read_file(path)` | Read a file relative to the repo root |
| `write_file(path, content)` | Write/overwrite a file |
| `run_bash(command)` | Run a shell command in the repo directory |
| `list_files(path)` | List directory contents |
| `signal_complete(next_label, summary)` | Finish and request label transition |
| `request_human(message)` | Pause for human input |

### `internal/workflow/engine.go`

```go
type TransitionTrigger string

const (
    TriggerAgent TransitionTrigger = "agent"
    TriggerHuman TransitionTrigger = "human"
)

type Engine struct {
    db  storage.DB
    hub *ws.Hub
}

// Transition validates and executes a label change. Publishes a WS event on success.
// Returns ErrNoTransition if (from→to) is not defined in the workflow.
// Returns ErrGateRequired if the transition requires a human but trigger is agent.
func (e *Engine) Transition(ctx context.Context, taskID, toLabel string, trigger TransitionTrigger, actorID, note string) error

// AvailableTransitions returns labels reachable from the task's current label,
// filtered to those allowed for the given trigger type.
func (e *Engine) AvailableTransitions(ctx context.Context, taskID string, trigger TransitionTrigger) ([]string, error)

// AgentPickupLabels returns all labels in a workflow where trigger is 'agent' or 'both'
// and agent_ignore is false. Used by the dispatcher sweep.
func (e *Engine) AgentPickupLabels(ctx context.Context, workflowID string) ([]string, error)
```

### `internal/agent/dispatcher.go`

```go
type Dispatcher struct {
    pool     *Pool
    storage  storage.DB
    workflow *workflow.Engine
    interval time.Duration
}

// Run sweeps on interval, finding tasks eligible for agent pickup.
func (d *Dispatcher) Run(ctx context.Context)

// sweep finds tasks whose label is agent-triggerable and have no running AgentRun,
// selects the matching AgentConfig, creates a pending AgentRun, submits to pool.
func (d *Dispatcher) sweep(ctx context.Context)
```

### `internal/agent/pool.go`

Bounded goroutine pool. Jobs exceeding `maxWorkers` wait in a buffered channel.

```go
type Job struct {
    RunID    string
    Provider Provider
    Input    RunInput
}

type Pool struct {
    maxWorkers int
    jobs       chan Job
    hub        *ws.Hub
    storage    storage.DB
    workflow   *workflow.Engine
}

func (p *Pool) Submit(job Job)
func (p *Pool) Start(ctx context.Context)
```

On job completion the pool calls `workflow.Transition` with the agent's `Result.NextLabel`.
If `Result.Status` is `waiting_human`, the task's `AgentRun.status` is set to `waiting_human`
and a `task.needs_human` WS event is published.

### `internal/ws/hub.go`

```go
type EventType string

const (
    EventTaskLabelChanged EventType = "task.label_changed"
    EventTaskAgentStarted EventType = "task.agent_started"
    EventTaskAgentDone    EventType = "task.agent_done"
    EventTaskNeedsHuman   EventType = "task.needs_human"
    EventAgentLog         EventType = "agent.log"
    EventTaskCreated      EventType = "task.created"
    EventTaskUpdated      EventType = "task.updated"
)

type Event struct {
    Type    EventType   `json:"type"`
    Payload interface{} `json:"payload"`
}

type Hub struct {
    clients        map[*Client]bool
    subscriptions  map[string]map[*Client]bool // task_id → clients
    broadcast      chan Event
    mu             sync.RWMutex
}

func (h *Hub) Publish(e Event)
func (h *Hub) Run(ctx context.Context)
```

Clients send a subscription message to filter `agent.log` events by task:
```json
{ "type": "subscribe_task",   "task_id": "abc123" }
{ "type": "unsubscribe_task", "task_id": "abc123" }
```

All other event types are broadcast to every connected client.

### `internal/git/diff.go`

Shells out to `git`. Returns structured types the frontend renders directly.

```go
type FileDiff struct {
    Path    string  `json:"path"`
    Status  string  `json:"status"`    // modified | added | deleted | renamed
    OldPath *string `json:"old_path"`
    Hunks   []Hunk  `json:"hunks"`
}

type Hunk struct {
    Header string `json:"header"`
    Lines  []Line `json:"lines"`
}

type Line struct {
    Type    string `json:"type"`    // context | added | removed
    Content string `json:"content"`
    OldNo   *int   `json:"old_no"`
    NewNo   *int   `json:"new_no"`
}

func Diff(repoPath, base, head string) ([]FileDiff, error)
func Tree(repoPath, ref string) ([]TreeEntry, error)
```

---

## API Routes

```
Tasks
  GET    /api/v1/tasks                       ?label=&repo_id=&type=
  POST   /api/v1/tasks
  GET    /api/v1/tasks/:id
  PATCH  /api/v1/tasks/:id                   title, description, type
  DELETE /api/v1/tasks/:id
  PATCH  /api/v1/tasks/:id/label             { to_label, note } — human-initiated move
  POST   /api/v1/tasks/:id/approve           { note } — approve a human gate
  POST   /api/v1/tasks/:id/reject            { note, to_label } — reject with feedback

Agent Runs
  GET    /api/v1/tasks/:id/runs
  GET    /api/v1/tasks/:id/runs/:run_id
  GET    /api/v1/tasks/:id/runs/:run_id/logs
  DELETE /api/v1/tasks/:id/runs/:run_id      cancels a running agent (context cancel)

Workflows
  GET    /api/v1/workflows
  POST   /api/v1/workflows
  GET    /api/v1/workflows/:id
  PUT    /api/v1/workflows/:id
  DELETE /api/v1/workflows/:id

Agent Configs
  GET    /api/v1/agents
  POST   /api/v1/agents
  GET    /api/v1/agents/:id
  PUT    /api/v1/agents/:id
  DELETE /api/v1/agents/:id

Repos
  GET    /api/v1/repos
  POST   /api/v1/repos                       { name, path, remote_url, workflow_id }
  GET    /api/v1/repos/:id
  DELETE /api/v1/repos/:id
  GET    /api/v1/repos/:id/tree              ?ref=HEAD
  GET    /api/v1/repos/:id/diff              ?base=HEAD~1&head=HEAD

Dashboard
  GET    /api/v1/dashboard                   label counts, active runs, intervention queue, feed

WebSocket
  WS     /ws
```

---

## WebSocket Event Payloads

```typescript
type WSEvent =
  | { type: "task.label_changed"; payload: { task_id: string; from: string; to: string; note?: string } }
  | { type: "task.agent_started"; payload: { task_id: string; run_id: string; agent_name: string } }
  | { type: "task.agent_done";    payload: { task_id: string; run_id: string; status: string } }
  | { type: "task.needs_human";   payload: { task_id: string; run_id: string; message: string } }
  | { type: "agent.log";          payload: { task_id: string; run_id: string; entry: LogEntry } }
  | { type: "task.created";       payload: Task }
  | { type: "task.updated";       payload: Task }
```

---

## Frontend Pages

### BoardPage — Kanban

- Columns ordered by `workflow_labels.sort_order`
- `not_ready` column visually muted; cards show a lock icon
- Card shows: title, type badge, label color, spinning ring if agent is currently running
- Drag-and-drop between columns via `@dnd-kit/core`; illegal moves (rejected by workflow engine) snap back with an error toast
- New task button opens a slide-over form

### DashboardPage

- **State count row** — one chip per label with count badge; clicking filters the board
- **Active agents table** — run_id, task title, agent name, elapsed timer, last log line (live via WS)
- **Intervention queue** — tasks in `waiting_human` state; inline approve/reject with optional note field
- **Activity feed** — chronological `task_label_history` events across all tasks, live-appended via WS

### TaskDetailPage — Three-panel layout

**Left panel** — Task metadata
- Editable title and description
- Type selector, label badge
- Label history timeline

**Center panel** — Agent log stream
- Virtualized list (react-virtual), auto-scrolls to bottom
- Color-coded rows by log type
- `tool_call` / `tool_result` rows collapsed by default, expandable
- Run selector to view historical runs

**Right panel** — File changes
- File tree (full repo, changed files highlighted)
- Click a file to expand inline diff below the tree using `react-diff-viewer-continued`

**Approval panel** — slides up from the bottom when task is at a human gate or `waiting_human`
- Shows agent summary message
- Approve → `POST /tasks/:id/approve`
- Reject → note field + target label selector → `POST /tasks/:id/reject`

### WorkflowPage — Visual editor (React Flow)

- Label nodes (colored, flag icons for agent_ignore / terminal)
- Directed edges labeled with trigger type
- Click edge → configure trigger type + assigned agent config
- Add/remove nodes and edges
- Save → `PUT /workflows/:id`
- Validation: no orphan nodes, terminal nodes have no outgoing agent transitions

### AgentConfigPage

- List with provider badge
- Create/edit slide-over:
  - Provider selector
  - Model input (free text, provider-aware suggestions)
  - System prompt (Monaco Editor)
  - Label assignment multi-select
  - Env var key/value table
  - Timeout and max_tokens inputs

---

## Implementation Phases

### Phase 1 — Foundation
- Go module, Chi router, CORS + logging + recovery middleware, `/healthz` endpoint
- Vite + React + TypeScript + Tailwind init
- SQLite connection, `golang-migrate` runner, `001_init.sql`
- `sqlc.yaml` config, codegen scaffold
- `TASKS.md` with `dev`, `gen`, `migrate`, `build` tasks
- `.air.toml` for hot reload

### Phase 2 — Storage & Workflow Engine
- All sqlc query files and codegen
- `storage.seed` — inserts default workflow if none exists
- `workflow.Engine` — `Transition`, `AvailableTransitions`, `AgentPickupLabels`
- Unit tests for workflow engine (legal transitions, gate enforcement, feedback loops)

### Phase 3 — REST API
- All CRUD handlers: tasks, workflows, agent configs, repos
- Workflow engine wired into task label change endpoints
- Approve / reject endpoints
- Dashboard aggregation query and handler
- OpenAPI spec written; `openapi-typescript` codegen wired into `TASKS.md`

### Phase 4 — Frontend Shell
- React Router (`/`, `/tasks/:id`, `/workflow`, `/agents`, `/dashboard`)
- Zustand stores for tasks, workflow, agents
- Nav sidebar
- Board page with static columns and task cards (data from API, no real-time yet)
- Task detail page (static — no log stream yet)

### Phase 5 — Agent Runtime
- `Provider` interface
- `ClaudeRunner` — subprocess spawn, stdout/stderr streaming goroutines, stream-json NDJSON parser
- `MCPManager` — starts per-run stdio MCP sidecar, writes temp config file, reads result channel
- MCP sidecar `cmd/mcp-server` — implements `signal_complete` and `request_human` over stdio
- `GenericLLMRunner` — HTTP tool-use loop, same two signal tools defined inline
- `Pool` — bounded goroutine pool
- `Dispatcher` — sweep loop, task pickup, AgentRun lifecycle, pool submission
- Agent log persistence (batched writes every 500ms or 50 lines)

### Phase 6 — WebSockets & Real-time
- `Hub` + `Client` (read/write pumps, ping/pong keepalive)
- WS upgrade handler at `/ws`, task subscription filtering
- Publish events from workflow engine and pool
- Frontend `ws.ts` typed client
- Live Kanban: cards move columns on `task.label_changed`
- Active agent ring indicator on cards

### Phase 7 — Task Detail Live View
- `AgentLogStream` — virtualized list, color-coded, tool_call rows collapsible
- Subscribe to `agent.log` events filtered to current task
- Historical logs loaded from API on mount; live events appended
- Approval panel slides up on `task.needs_human` or human-gate label detection

### Phase 8 — Git Integration
- `git.Diff` — shells out to `git diff`, parses unified diff into `[]FileDiff`
- `git.Tree` — shells out to `git ls-tree -r --name-only`, returns `[]TreeEntry`
- `/repos/:id/diff` and `/repos/:id/tree` endpoints
- `FileDiffViewer` component — file list, click to expand inline diff
- Repo file tree sidebar in task detail, changed files highlighted

### Phase 9 — Dashboard
- `GET /api/v1/dashboard` aggregation
- Dashboard page: stat chips, active agent table with live elapsed timer, intervention queue with inline approve/reject, activity feed
- Activity feed subscribes to WS for live append

### Phase 10 — Drag-and-Drop Board
- `@dnd-kit/core` integration on Kanban columns
- Optimistic update on drop, revert on API error
- Error toast for illegal transitions

### Phase 11 — Workflow Editor
- React Flow integration
- Custom node and edge types
- Load/save workflow from/to API
- Add/remove nodes and edges
- Transition edge config panel (trigger type, agent config selector)

### Phase 12 — Agent Config UI
- Agent config list and create/edit slide-over
- Monaco Editor for system prompt
- Provider-aware model suggestions
- Label assignment picker
- Env var table editor

### Phase 13 — Extensibility
- `TaskSource` interface abstracting SQLite task storage
- GitHub Issues adapter stub (mirrors GH issues to local SQLite)
- YAML export/import for workflow config
- `~/.agent-editor/config.yaml` for server port, DB path, CORS origins
- Optional bearer token for remote frontend → backend connections

---

## Key Design Decisions

**Agent idempotency** — Before the dispatcher creates a new `AgentRun`, it checks
`tasks.current_agent_run_id` and only proceeds if that run is not in `running` state.
Prevents double-dispatch if the sweep fires while an agent is mid-run.

**Context cancellation** — Every subprocess is started with a derived `context.Context`.
`DELETE /tasks/:id/runs/:run_id` cancels the context, which sends SIGKILL to the process
via `exec.CommandContext`. The MCP sidecar's cleanup function is deferred so it always exits.

**Log batching** — Agent log lines are written to SQLite in batches (every 500ms or 50 lines)
to avoid a DB write per line, while still being published to the WS hub immediately for
live streaming with zero added latency.

**Feedback injection** — When a task is rejected at `review` and returns to `in-progress`,
the rejection note is stored on the new `AgentRun` as `feedback`. Both runners prepend this
to the user message so the agent has full context on what was wrong in the prior run.

**Workflow isolation** — Each repo has its own `workflow_id`. Multiple repos can share a
workflow definition or have dedicated ones. Agent configs are global and referenced by
workflow transition rules.

**MCP sidecar lifetime** — The sidecar starts immediately before `cmd.Start()` and its
cleanup (closing the stdio pipes, waiting for the process to exit) runs in a `defer` before
`ClaudeRunner.Run` returns. The result channel is buffered (size 1) so the sidecar can write
its result and exit without blocking even if the runner hasn't read it yet.

**Remote access** — The backend is a plain HTTP + WebSocket server. Setting `CORS_ORIGINS`
in config allows the frontend to be served from a different host/machine. An optional
`AUTH_TOKEN` config value gates all API and WS requests behind a bearer token check in
middleware, enabling secure remote use without a full auth system.
