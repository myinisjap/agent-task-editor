# 35. CI gates: generated artifacts, coverage floors, e2e isolation

Status: Accepted

## Context

Several files are generated and drift silently when someone forgets to
regenerate. Coverage numbers can also mislead if generated or glue code counts.

## Decision

- CI regenerates sqlc output, `frontend/src/api/types.ts` (from `openapi.yaml`)
  and the `docs/agents.md` capability table, and fails on any diff.
- Backend coverage (`go test -race -coverprofile ... -covermode=atomic`) is
  floor-gated after filtering out `internal/storage/gen/` (generated) and
  `cmd/server` (wiring and shutdown glue, not meaningfully unit-testable).
  Local totals therefore read a few points lower than CI's.
- Frontend coverage uses vitest v8 with `all: true` and
  `include: ['src/**/*.{ts,tsx}']` so brand-new untested files count as 0%
  instead of being absent; excluded: tests, generated `types.ts`, `main.tsx`,
  `src/test/**`. The floor is on statements.
- `govulncheck ./...` is blocking; the toolchain version (not the `go.mod`
  directive) determines the stdlib it analyzes.
- Playwright e2e runs against the real built compose stack (nothing mocked) in
  its own CI job so a slow or flaky run never gates unit-test feedback; every
  spec runs under desktop `chromium` and `mobile-chrome` projects.

## Consequences

- Run the relevant generator and commit its output with the source change.
- Component tests mock `api/client`, seed stores with `.setState`, stub
  `wsClient`, and simulate dnd-kit with real mouse events plus
  `getBoundingClientRect` stubs.
