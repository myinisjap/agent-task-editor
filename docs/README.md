# Agent Task Editor — Docs

Agent Task Editor is a self-hosted Kanban board where AI agents automatically work through tasks as they move across workflow columns.

## Contents

| File | Description |
|---|---|
| [overview.md](overview.md) | Architecture, core concepts, and feature summary |
| [getting-started.md](getting-started.md) | Installation, environment variables, and first steps |
| [workflows.md](workflows.md) | Workflow state machine: labels, transitions, approve/reject |
| [agents.md](agents.md) | Agent configs, providers, dispatcher, worker pool, prompt construction |
| [providers/](providers/) | Per-provider deep-dives (credentials, MCP support, limitations) |
| [mcp-tools.md](mcp-tools.md) | MCP sidecar tool reference (for `claude` and `qwen_code` providers) |
| [task-sources.md](task-sources.md) | Importing GitHub Issues as tasks |
| [api.md](api.md) | REST API reference |
| [websocket.md](websocket.md) | WebSocket protocol and event types |

## Quick Start

```bash
git clone https://github.com/myinisjap/agent-task-editor
cd agent-task-editor
./dev.sh start        # Docker Compose (recommended)
# or
./dev.sh dev          # local processes (Go + Node, no Docker)
```

Open `http://localhost:5173` in your browser.
