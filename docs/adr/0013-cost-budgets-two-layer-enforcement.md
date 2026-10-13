# 13. Cost budgets: pre-dispatch guard plus mid-run kill switch

Status: Accepted

## Context

Spend must be bounded per task, but only some providers report priced usage
while streaming. The CLIs report authoritative cost only at run end, which is
too late to stop an overrun.

## Decision

Budget is the lower nonzero of `AgentConfig.MaxCostUSD` and task
`max_cost_usd` (`effectiveBudget`).

- Pre-dispatch guard (all providers): `Dispatcher.checkCostBudget` sums
  `SumTaskCost` across the task's runs before each dispatch. If spent >= budget
  it creates a phantom `waiting_human` run, locks the task and publishes
  `task.needs_human`. It never touches an in-flight run.
- Mid-run kill (claude and priced qwen only): the provider's `costWatchdog`
  projects total cost from incremental token usage via `pricing.go`, cancels
  its own subprocess and returns `*agent.ErrCostBudgetExceeded`.
  `handleProviderError` checks it before the transient branch and
  `handleCostBudgetExceeded` marks the run `waiting_human` (never retried:
  re-dispatch would spend against the same exhausted budget). The provider
  fills `InputTokens`/`OutputTokens`/`CostUSD` from the watchdog snapshot (this
  run's increment = projected total minus `RunInput.CostSpentUSD`) because the
  killed run never emits a terminal `result`; otherwise `SumTaskCost` would
  undercount and a kill, raise-budget, resume cycle would reset the ledger.
- Early warning: `task.cost_warning` fires once spend crosses the global
  `warn_ratio` (default 0.8, `/settings/cost-warning`), gated per task by the
  one-shot `cost_warned` column, reset when `max_cost_usd` changes.
- Providers learn the budget only through `RunInput.CostBudgetUSD`/
  `CostSpentUSD`/`CostWarnRatio`, set in `startRun`; they never query storage.

## Consequences

- Do not wire a watchdog into a provider without priced, incremental usage
  before the terminal event. codex prices only at `turn.completed`; opencode's
  `step_finish` is a cumulative snapshot. Both rely on the pre-dispatch guard.
