# 9. Ref-mutating git calls take a per-repo, non-reentrant lock

Status: Accepted

## Context

Git worktrees share one ref store. A commit in one worktree racing a merge,
branch delete or provision in another fails with errors such as
`cannot lock ref 'HEAD'` (issue #344).

## Decision

`RepoGitLock` (`worktree.go`) serializes ref-mutating git per repo. Every call
site takes it around its git calls: the pool's safety-net commit and push, the
subtask coordinator's merge and teardown, the dispatcher's worktree
provisioning (`git fetch --prune` + `git worktree add -b`), `main.go`'s
`engine.OnTerminal`, and the worktree sweeper's reclaim pass. The mutex is
plain and **not reentrant**: callers take it, helpers
(`provisionWorktree(From)`, `RemoveWorktree`, `PushBranch`) never do, because
the coordinator already holds it across calls into them. Lock order is always
parent lock then repo lock, and only the coordinator takes the parent lock.

## Consequences

- Adding a git helper: do not lock inside it; lock at the call site.
- Adding a new ref-mutating call site: take the repo lock.
- No lock cycles as long as the ordering holds.
