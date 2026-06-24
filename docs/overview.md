# Agent Task Editor — Overview

Agent Task Editor is a self-hosted Kanban-style task board where AI agents automatically work through tasks as they move across workflow columns. You define the workflow, assign AI agents to specific columns, and the system dispatches, runs, and tracks every agent invocation against your code repositories.

## Core Concepts

### Tasks
A task is the unit of work — it has a title, a description, a type (e.g. `feature`, `bug`, `chore`), and a **label** that represents its current position in the workflow. Tasks are always associated with a repository and a workflow.

### Workflows
A workflow is a directed state machine composed of **labels** (columns) and **transitions** (allowed moves between them). Each transition has a `trigger_type`:

- `agent` — an AI agent may execute this transition automatically
- `human` — only a human clicking Approve/Reject in the UI may execute it
- `both` — either can trigger it

Labels carry flags:
- `agent_ignore` — agents may not send tasks here (useful for parking spots like `not_ready`)
- `is_terminal` — the task is finished; no further transitions apply (e.g. `done`)
- `is_rejection_target` — the workflow-defined destination when a human clicks Reject (e.g. `in-progress`)

### Default Workflow
The server seeds a default workflow on first run:

```
not_ready → plan → todo → in-progress → testing → agent-review → review → done
```

Key behaviour:
- `plan → todo` requires human approval
- `todo → in-progress` is agent-triggered (dispatch kicks off automatically)
- `review → done` requires human approval
- Feedback loops exist: any stage can move back to `in-progress` (agents or humans)
- `not_ready` has `agent_ignore = true` — nothing runs while a task is parked there

### Agent Configs
An agent config binds a set of labels to a specific AI provider with its settings (model, system prompt, token limits, timeout, extra env vars). When the dispatcher finds a task on one of those labels it creates an agent run using that config.

### Repositories
A repository is a local directory path registered in the system. Agents run with their working directory set to the repo path. Optionally, `REPO_BASE_DIR` restricts which paths can be registered.

### Agent Runs
Each time an agent is dispatched for a task, an **agent run** record is created. Runs have statuses: `pending → running → completed | failed | waiting_human`. Live stdout/tool call output is streamed over WebSocket and persisted to the database for replay on reconnect.

## Architecture

```
Browser ──── REST + WebSocket ──── Backend (Go)
                                       │
                               ┌───────┴────────┐
                               │                │
                          Dispatcher          Worker Pool
                          (5-sec sweep)      (N goroutines)
                               │                │
                          SQLite DB        Agent Provider
                                          ┌──────────────┐
                                          │ ClaudeRunner  │ (claude CLI + MCP)
                                          │ AnthropicRunner│ (Messages API)
                                          │ LLMRunner     │ (OpenAI-compat)
                                          └──────────────┘
```

The dispatcher polls the database every 5 seconds for tasks whose label matches an agent config. It creates a run record, marks the task's `active_agent_run_id`, and submits the job to the bounded worker pool. The pool streams logs to the WebSocket hub as they arrive and persists them to SQLite in batched transactions.

## Features at a Glance

- **Kanban board** with drag-and-drop between columns
- **Live log streaming** — agent stdout, tool calls, and tool results streamed in real time
- **Log replay** — reconnecting clients receive all prior logs for the current run
- **Workflow editor** — create/edit labels, transitions, trigger types; import/export YAML
- **Agent config UI** — manage multiple AI configs each targeting different workflow stages
- **Git diff viewer** — per-repo diff of uncommitted changes
- **Dashboard** — run counts, completion rate, recent activity
- **Bearer token auth** — optional `API_TOKEN`; WebSocket auth via `?token=` query param
- **Docker Compose deployment** — single `docker compose up` to run everything
