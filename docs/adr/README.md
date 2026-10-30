# Architecture Decision Records

Short records (Title, Status, Context, Decision, Consequences) of the design
decisions behind this codebase and why they were made. The `AGENTS.md` files
state the current rules; these records hold the rationale and history behind
them. Read the relevant ADR before changing the behavior it covers.

To add one: copy the shape of an existing record, use the next number, and link
it from the relevant `AGENTS.md`. Do not rewrite an accepted record to change a
decision; add a new one and mark the old one `Superseded by NNNN`.

| # | Decision |
|---|---|
| [0001](0001-active-vs-current-agent-run-id.md) | Separate `active_agent_run_id` from `current_agent_run_id` |
| [0002](0002-waiting-human-keeps-dispatch-lock.md) | `waiting_human` runs keep the dispatch lock |
| [0003](0003-dispatch-lock-compare-and-swap.md) | Dispatch lock is claimed and released with compare-and-swap, scoped to the owner |
| [0004](0004-mcp-sidecar-result-file-handoff.md) | MCP sidecar hands results to the runner through a result file |
| [0005](0005-repo-base-dir-and-auth-are-warn-only.md) | `REPO_BASE_DIR` and API auth are optional, with startup warnings |
| [0006](0006-agent-toolchain-and-runtime-pins.md) | Agent toolchains: image baseline plus per-repo runtime pins |
| [0007](0007-provider-capabilities-single-source-of-truth.md) | `providerCapabilities.ts` is the single source of truth for provider gaps |
| [0008](0008-per-task-git-worktrees.md) | Each task works in its own git worktree and branch |
| [0009](0009-per-repo-git-lock.md) | Ref-mutating git calls take a per-repo, non-reentrant lock |
| [0010](0010-single-failure-classification-table.md) | One classification table for provider failures |
| [0011](0011-retry-budgets-and-failure-counters.md) | Separate retry budgets for transient and genuine failures |
| [0012](0012-cancel-pauses-the-task.md) | Cancelling a run pauses the task |
| [0013](0013-cost-budgets-two-layer-enforcement.md) | Cost budgets: pre-dispatch guard plus mid-run kill switch |
| [0014](0014-untrusted-content-fencing.md) | Third-party content is write-access filtered and fenced in prompts |
| [0015](0015-rework-loop-feedback-and-circuit-breaker.md) | Failure findings become feedback; repeated rework loops escalate |
| [0016](0016-subtask-merge-back-coordinator.md) | Subtasks merge back through a per-parent coordinator |
| [0017](0017-providers-package-and-one-parser-per-provider.md) | `agent` defines the contract; `providers` implements it; one parser file per provider |
| [0018](0018-provider-configs-and-provider-deprecation.md) | Provider configs are a separate entity; `anthropic`/`llm` are deprecated, then removed in v0.17.0 |
| [0019](0019-agent-env-allowlist.md) | Agent subprocesses get an allow-listed environment |
| [0020](0020-sqlc-constraints.md) | sqlc query-writing constraints |
| [0021](0021-utc-timestamp-storage.md) | All timestamps are stored as UTC `YYYY-MM-DD HH:MM:SS` |
| [0022](0022-websocket-ticket-auth.md) | WebSocket auth uses single-use tickets; `/ws` is mounted outside bearer middleware |
| [0023](0023-websocket-bounded-delivery-and-replay.md) | WebSocket delivery is bounded: drop-and-resync, tail replay, timeouts |
| [0024](0024-separate-board-mcp-binary.md) | `cmd/mcp-board` is a separate binary from the per-run sidecar |
| [0025](0025-cursor-pagination.md) | List endpoints use id-cursor pagination with a plain-array body |
| [0026](0026-graceful-shutdown-and-interrupted-status.md) | Shutdown and crashes produce `interrupted` runs, not failures |
| [0027](0027-derived-state-over-stored-state.md) | Dispatch gates are independent columns or derived, never inferred from label |
| [0028](0028-session-resume.md) | Only `claude` resumes sessions, with a condensed prompt |
| [0029](0029-workflow-engine-transitions.md) | The workflow engine is the only writer of label transitions |
| [0030](0030-http-api-conventions.md) | HTTP API conventions: JSON everywhere, ordered guards, safe uploads |
| [0031](0031-db-backed-runtime-settings.md) | Runtime-tunable settings live in the database; env vars only seed them |
| [0032](0032-frontend-runtime-api-token.md) | The frontend API token is a runtime value in localStorage |
| [0033](0033-frontend-theming-via-css-variables.md) | Light theme is derived by remapping Tailwind CSS variables |
| [0034](0034-frontend-stores-sequence-requests.md) | Frontend stores sequence requests and merge WebSocket updates |
| [0035](0035-ci-quality-gates.md) | CI gates: generated artifacts, coverage floors, e2e isolation |
| [0036](0036-idempotent-ingestion-and-explicit-repo-policy-columns.md) | External ingestion is idempotent by external id; repo policy columns are always written explicitly |
| [0037](0037-task-git-and-writeback-side-tables.md) | Git/PR state and write-back flags live in side tables, read through the `tasks_full` view |
| [0038](0038-authoritative-verify-command.md) | A repo verify command (with per-label override) is authoritative over the agent's success claim |
| [0039](0039-semantic-design-tokens.md) | Semantic OKLCH design tokens and UI primitives coexist with the generated light remap |
| [0040](0040-llm-proposes-autonomy-rules-never-decides.md) | A model may propose autonomy rules and flags; it never decides a gate |
