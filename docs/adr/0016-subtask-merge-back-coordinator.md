# 16. Subtasks merge back through a per-parent coordinator

Status: Accepted

## Context

Agent-driven decomposition (issue #82) creates child tasks with their own
branches. Their work has to land on the parent branch without corrupting the
parent worktree, and the parent should advance exactly once when all children
are done.

## Decision

`SubtaskCoordinator` (`subtasks.go`) owns the child to parent lifecycle.
`engine.OnTerminal` calls `OnChildTerminal`; `pool.Subtasks` calls
`AfterParentRun`; `dispatcher.Subtasks` injects conflict context via
`BuildConflictContext`.

- On child terminal the child branch is merged into the parent branch with a
  `--no-ff` merge commit. On success the child worktree and local branch are
  removed (children never push to origin). A conflict is aborted cleanly and
  the child is flagged `merge_status=merge_conflict`.
- Auto-advance requires every non-archived child to be terminal **and**
  `merge_status=merged`. A terminal-but-unmerged sibling (its merge queued
  behind the parent lock) must not trigger a premature advance, so the advance
  happens exactly once, when the last merge lands. It uses
  `workflow.TriggerSubtasksComplete`, which bypasses human/agent gate checks
  because the coordinator selects an already-validated agent-success target.
- A per-parent mutex serializes merge-back so simultaneous children merge one
  at a time; ordering relative to the repo lock is in ADR 0009.
- If the parent has a run in flight, the merge is marked `pending` and flushed
  by `AfterParentRun`.
- `create_subtask` inserts the child and the parent-to-child dependency edge
  in one transaction (ADR 0004 for why it is a live POST).

## Consequences

- Tests: `subtasks_e2e_test.go` (real dispatcher, pool, engine, coordinator
  over a temp git repo) and `subtasks_coord_test.go`.
