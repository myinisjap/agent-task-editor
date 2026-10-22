# 11. Separate retry budgets for transient and genuine failures

Status: Accepted

## Context

Infra hiccups should be retried quietly; genuine task failures and stuck states
should reach a human without spinning forever. A 429 also needs to throttle the
whole agent config, which is a different scope from one task's budget.

## Decision

- Transient failures: `tasks.transient_retry_count` and `tasks.next_retry_at`.
  Under budget (`AgentConfig.MaxRetries`, default 3; 0 escalates on the first),
  `handleTransientFailure` sets exponential backoff
  (`BackoffDurationWithBase`, `retry_backoff_secs` default 30, cap 10m) and
  clears the lock; `ListAgentPickupTasks` hides tasks whose `next_retry_at` is
  in the future. At exhaustion the task escalates to `waiting_human` and the
  counter resets.
- Genuine failures: `tasks.consecutive_failure_count`, limit
  `maxConsecutiveFailures` (3), then `waiting_human`. Reset by a completed run,
  any label transition, `DispatchReply` and `Rerun`. Transient failures neither
  increment nor reset it, so alternating transient and genuine failures still
  escalate. Genuine failures and successes do reset the transient counter.
- `auth` classification escalates immediately instead of retrying.
- A memguard cancel sets `next_retry_at` (cooldown) with `transient_retry_count = 0`; it spends neither budget. The growth streak and the global dispatch pause are in memory only.
- A run that completes with no resolvable outcome escalates once
  (`escalateNoOutcome`, lock kept, single `task.needs_human`).
- `RateLimitRegistry` blocks the whole agent config on 429 and works
  independently of the per-task budget. Post-run cleanup uses
  `UnblockIfNotBlockedSince(cfgID, startedAt)` rather than `Unblock`, so with
  `MAX_WORKERS > 1` a run that started before a sibling's fresh 429 cannot wipe
  that block or the consecutive-429 counter (issue #344). Blocks are written
  through to the `agent_rate_limits` table and reloaded (unexpired only) at
  startup so a restart does not forget a fresh 429; store errors only log.
- Claude "resets 6pm (TZ)" text is parsed to an exact `ResetAt` for retry
  scheduling; the binary embeds `time/tzdata` because the prod image has no
  zoneinfo.

## Consequences

- New failure paths must pick a counter deliberately and keep the lock on
  escalation (ADR 0002).
