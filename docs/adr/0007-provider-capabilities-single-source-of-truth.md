# 7. `providerCapabilities.ts` is the single source of truth for provider gaps

Status: Accepted

## Context

Providers differ (MCP support, label transitions, cost tracking, command
allow/denylist, ...). Warnings in several forms, and a capability table in the
docs, drifted from each other when each was edited by hand.

## Decision

`frontend/src/lib/providerCapabilities.ts` defines the capability matrix.
`AgentConfigForm`, `ProviderConfigForm` and `CommandFilterEditor` read it to
show inline warnings. The Capability Matrix table in `docs/agents.md` is
generated from it by `npm run gen:capability-docs` between the
`<!-- BEGIN/END capability-matrix (generated) -->` markers. CI re-runs the
generator and fails if `docs/agents.md` differs.

## Consequences

- Change capabilities in the TS file, then regenerate; never hand-edit the
  generated table.
- Forgetting to regenerate fails CI instead of silently drifting.
- The same pattern (generate, commit, CI diff-check) is used for sqlc output
  and `types.ts` from `openapi.yaml`.
