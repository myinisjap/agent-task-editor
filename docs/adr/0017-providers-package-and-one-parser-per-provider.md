# 17. `agent` defines the contract; `providers` implements it; one parser file per provider

Status: Accepted

## Context

The runtime core (pool, dispatcher) must not depend on any concrete backend,
and provider-specific wire-format parsing was a source of cross-provider
breakage when several providers shared a file.

## Decision

- `internal/agent` defines `Provider`, `RunInput`, `Result`, shared errors and
  classification. `internal/agent/providers` imports `agent`; the reverse is
  forbidden. Concrete runners are constructed only in `cmd/server/main.go`'s
  `providerFactory`.
- Each provider's parsing lives in exactly one `parse_<name>.go`. A runner calls
  only its own parser entry point. `parse_streamjson.go` is shared wire-format
  code owned by no provider, reached via `claude.go` directly (it is claude's
  native format) or through `parse_qwen.go`'s thin `classifyQwenJSON`
  delegation (qwen reuses claude's envelope today).
- Stream-json classification passes the **raw line** through as `Content`;
  display shaping is the frontend's job (`parseAgentLog.ts`).

## Consequences

- Per-provider parsing changes land in one file without touching siblings.
- A new provider adds `parse_<name>.go` if it needs its own parsing.
