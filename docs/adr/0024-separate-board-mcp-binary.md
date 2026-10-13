# 24. `cmd/mcp-board` is a separate binary from the per-run sidecar

Status: Accepted

## Context

Chat clients (e.g. Claude Desktop) should be able to create board tickets.
Putting `create_task` in the per-run sidecar would hand every flow agent the
ability to create arbitrary tasks.

## Decision

`cmd/mcp-server` is ephemeral and task-scoped (env-injected
`RUN_ID`/`TRANSITIONS`/`RESULT_FILE`). `cmd/mcp-board` is a long-lived process a
human points a chat client at; it has no run context and just calls the backend
REST API (`list_repos`, `list_workflows`, `create_task`). Flow agents never
see its tools; that is the access boundary the binary exists to enforce.
`create_task` omits `workflow_id` when not given and lets the backend apply its
default (`resolveDefaultWorkflowID`: workflow named "Default", else
alphabetically first), not one derived from the repo; it defaults `label` to
`work`. Both speak newline-delimited JSON-RPC over stdio with a 4 MB scanner
buffer.

## Consequences

- Board-level tools belong in `mcp-board`, run-level tools in `mcp-server`.
- Chat sessions get board tools only when `MCP_BOARD_PATH` is set.
