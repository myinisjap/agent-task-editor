# 4. MCP sidecar hands results to the runner through a result file

Status: Accepted

## Context

The agent CLI (`claude -p` and friends) launches the MCP sidecar
(`cmd/mcp-server`) itself as a stdio child from `--mcp-config`. The backend
process has no direct channel to that child. The backend needs the agent's
outcome (`signal_complete`, `request_human`, notes, resolved comments) after
the CLI exits.

## Decision

`MCPManager.Prepare(runID)` writes a temp MCP config and sets `RESULT_FILE` to
a temp path. Tool calls accumulate state and the sidecar writes an
`agent.Result` JSON to `RESULT_FILE`. After the CLI subprocess exits, the runner
calls `ReadResult()` and returns it to the pool. Sidecar inputs (`RUN_ID`,
`TRANSITIONS`, `REVIEW_COMMENTS`, `SUBTASKS_ENABLED`, ...) are passed via env.
Review-comment resolutions travel the same way and are applied by the pool only
when the run completes successfully.

The one exception is `create_subtask`: it must show children on the board
mid-run and surface backend validation errors to the agent, so it POSTs to the
backend REST API live (and is exposed only when `SUBTASKS_ENABLED=1`).

## Consequences

- Results are visible only after the subprocess exits; a killed or crashed run
  yields no sidecar result.
- No sockets or ports are needed between backend and sidecar.
- The sidecar speaks only `initialize`, `tools/list`, `tools/call` and ignores
  notifications; adding a tool means updating the env contract in `mcp.go` too.
- `cmd/mcp-board` is a different binary for a different trust boundary
  (ADR 0024).
