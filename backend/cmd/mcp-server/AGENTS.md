# cmd/mcp-server

A minimal MCP (Model Context Protocol) server that the `claude` provider runs as a sidecar subprocess. Communication is over stdio using JSON-RPC 2.0 (newline-delimited JSON).

## Purpose

The `claude`, `qwen_code` and `codex_cli` providers run this as a sidecar over stdio (JSON-RPC 2.0, newline-delimited). It exposes tools that let the agent signal back to the orchestration system:

- `get_task_transitions()` — lists the valid outcome transitions from the task's current label
- `signal_complete(outcome, summary)` — marks the agent run as completed with outcome `success`/`failure`; the pool resolves the next workflow label
- `request_human(message)` — pauses the run and surfaces a message for human review before the agent continues
- `update_task_notes(notes, append)` — persists markdown notes on the task for subsequent agents
- `store_info(info)` — stores per-run info shown in the task detail view
- `resolve_comment(comment_id, note)` — marks an inline diff review comment (from the prompt's "OPEN REVIEW COMMENTS" section) as addressed; applied to the DB by the pool only if the run completes successfully
- `create_subtask(title, description, type)` — splits the task into a child (Mechanism 2). **Exposed only when `SUBTASKS_ENABLED=1`** (set by `MCPManager.Prepare` when the run's agent config has `subtasks_enabled`). Unlike the deferred result-file tools, it writes **live**: it `POST`s to `{BACKEND_URL}/api/v1/tasks/{TASK_ID}/subtasks` (with the `API_TOKEN` bearer when set) and returns the created task id synchronously, so children appear on the board mid-run. The backend enforces the cap / human-gate label / depth limit and returns the error text, which the tool surfaces back to the agent.

## How It Works (ADR 0004)

1. `MCPManager.Prepare(runID)` writes a temp MCP config pointing at this binary and sets `RESULT_FILE`.
2. The CLI launches the sidecar from `--mcp-config`.
3. `signal_complete`/`request_human` (and notes, resolutions) are written as an `agent.Result` JSON to `RESULT_FILE`.
4. After the CLI exits, `mcpCfg.ReadResult()` returns it to the pool.

## Environment Variables

| Variable | Description |
|---|---|
| `RUN_ID` | The agent run UUID (set by `MCPManager.Prepare`) |
| `RESULT_FILE` | Path where the result JSON is written (set by `MCPManager.Prepare`) |
| `TRANSITIONS` | JSON array of available transitions for `get_task_transitions` |
| `REVIEW_COMMENTS` | JSON array of open review comments, used to validate `resolve_comment` IDs |
| `SUBTASKS_ENABLED` | `"1"` when the run's config opted in; gates the `create_subtask` tool |
| `BACKEND_URL` / `TASK_ID` / `API_TOKEN` / `MAX_SUBTASKS` | Backend coordinates for `create_subtask`'s live POST (set only when subtasks are enabled) |

## Protocol Details

- Only `initialize`, `tools/list`, and `tools/call` methods are implemented
- Notifications (no `id` field) are silently ignored — no response sent
- Unknown methods return JSON-RPC error `-32601`
- Scanner buffer is 4 MB to handle large tool call payloads
