# 3. Dispatch lock is claimed and released with compare-and-swap, scoped to the owner

Status: Accepted

## Context

Several actors touch the lock concurrently: the 5s dispatcher sweep, the pool
finishing runs, human transitions, replies and reruns. Unconditional writes
caused real races: a finished run wiping a lock a newer re-dispatch had taken
(issue #244), a human move over a live run silently releasing the lock so the
next sweep double-dispatched onto the same worktree, and pre-clearing the lock
before a transition landed. The same ownership-bug class appeared in
`TerminalManager`'s session map.

## Decision

- Claim: `ClaimTaskActiveRunIfFree` (sweep) and `ClaimTaskActiveRunIfHeldBy`
  (human reply) run inside `persistRunRow`'s transaction and require that the
  task is still on the label the dispatcher read and the lock is still what it
  read. Zero rows affected rolls back the new run row and returns
  `ErrDispatchConflict` (sweep skips quietly; reply handler returns 409).
- Release on behalf of a run uses `ClearActiveAgentRunIfOwner` (requires the
  caller's own run id). The unscoped `ClearActiveAgentRun` is reserved for
  `Rerun`'s human force-clear. `SetTaskActiveRun`/`SetTaskActiveRunOnly` are
  for escalation bookkeeping on a lock the run already owns.
- On a `completed` run that transitions, the pool does not pre-clear the lock;
  `engine.Transition`'s CAS clears it atomically with the label move.
- Human `Approve`/`Reject`/`MoveLabel` and bulk `move` return 409 (or a failed
  per-task result) when the active run is `pending`/`running`
  (`rejectTransitionWhileRunLive`).
- Owner-scoped deletes apply to maps too: the terminal output pump only
  deletes its session from `m.sessions` if the map still holds the same
  `*ptySession`.

## Consequences

- Never replace these with an unconditional `SET active_agent_run_id`.
- Transitions are not allowed while a run is live; humans must cancel first.
- The engine's transition write is raw-SQL CAS for sqlc reasons (ADR 0020).
