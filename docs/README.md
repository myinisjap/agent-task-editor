# Agent Task Editor — Docs

Agent Task Editor is a self-hosted Kanban board where AI agents automatically work through tasks as they move across workflow columns.

## Contents

| File | Description |
|---|---|
| [overview.md](overview.md) | Architecture, core concepts, feature summary, and navigation (Inbox, Board, Chat, Pipeline, Repos, Settings, Insights) |
| [getting-started.md](getting-started.md) | Installation, environment variables, and first steps |
| [workflows.md](workflows.md) | Workflow state machine: labels, transitions, approve/reject, verify commands, Pipeline graph editor |
| [agents.md](agents.md) | Agent configs, providers, dispatcher, worker pool, prompt construction |
| [providers/](providers/) | Per-provider deep-dives — `claude`, `codex_cli`, `opencode`, `qwen_code`, (credentials, MCP support, limitations) |
| [mcp-tools.md](mcp-tools.md) | MCP sidecar tool reference (for `claude`, `qwen_code`, and `codex_cli` providers) |
| [runtime.md](runtime.md) | Per-repo agent toolchains via `mise`/`uv` — language version pins, detection, fail-closed behavior |
| [board-mcp.md](board-mcp.md) | Board MCP tools: in-app chat, a local chat client (e.g. Claude Desktop), or an opt-in claude.ai remote connector |
| [task-sources.md](task-sources.md) | Importing GitHub Issues as tasks, write-back, and the optional GitHub webhook |
| [task-templates.md](task-templates.md) | Task templates and recurring cron-scheduled task creation |
| [skills.md](skills.md) | Skills library: Agent Skills storage, API and per-provider materialization |
| [api.md](api.md) | REST API reference |
| [websocket.md](websocket.md) | WebSocket protocol and event types |
| [backup.md](backup.md) | Backup, restore, and continuous replication |
| [adr/](adr/README.md) | Architecture Decision Records: why the runtime, storage and API work the way they do |
| [screenshots.md](screenshots.md) | Runbook for regenerating the README/docs screenshots and hero GIF |

## Quick Start

```bash
git clone https://github.com/myinisjap/agent-task-editor
cd agent-task-editor
./dev.sh start        # Docker Compose (recommended)
# or
./dev.sh dev          # local processes (Go + Node, no Docker)
```

Open `http://localhost:5173` in your browser.
