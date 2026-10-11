# 15. Failure findings become feedback; repeated rework loops escalate

Status: Accepted

## Context

An `outcome='failure'` completion sends the task back along its failure
transition (e.g. `agent-review -> work`). Every transition clears the dispatch
lock, so the loop was unbounded. Worse, findings reached the next Worker only
as "NOTES FROM PRIOR AGENT" (an implementation plan); the Worker saw
already-committed code, judged it done and advanced without addressing the
findings, causing an observed infinite loop.

## Decision

- On a failure outcome the run summary is saved as the run's `feedback`
  (`SetAgentRunFeedback`). The next dispatch renders it under
  `FEEDBACK FROM PRIOR REVIEW`, a fix request distinct from `PriorPlan`.
- `failureLoopExceeded` counts the identical `(fromLabel -> toLabel)` agent
  transitions in the tail of `task_label_history`; the window resets on any
  human transition or any exit from `fromLabel` to a different label. At
  `failureLoopThreshold` (3) `escalateFailureLoop` re-writes the run
  `waiting_human` (usage preserved), keeps the lock and publishes
  `task.needs_human`.

## Consequences

- History-derived, so no new column or migration, and a human move naturally
  resets the budget.
- The completion path clears the lock only in branches that own that
  responsibility; escalation must not clear it before escalating.
