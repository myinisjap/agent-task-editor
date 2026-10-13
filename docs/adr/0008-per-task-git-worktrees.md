# 8. Each task works in its own git worktree and branch

Status: Accepted

## Context

Several agents run concurrently, often on the same repo. Sharing a checkout
causes conflicting edits, and the human needs a stable branch to review.

## Decision

First dispatch calls `provisionWorktree`:
`git worktree add -b ate-<slug>-<id8> <repo>/.ate-worktrees/<id> <baseRef>`.
Branch, path and base are stored on the task (`SetTaskWorktree`) and reused on
every re-run so feedback runs stack commits on one branch. The agent's
`RepoPath` (`cmd.Dir`) is the worktree, never the main clone.

- Base ref is `origin/HEAD`, else `origin/main`, else current `HEAD`, and is
  frozen on the task so the diff stays stable if the default branch moves.
- After a `completed` run, `commitIfDirty` makes a safety-net commit.
- Diff is `git diff merge-base(base, branch)..branch`.
- On a terminal label (`engine.OnTerminal`) the branch is pushed if the repo has
  a remote and the worktree is removed; the branch is kept for review.
- Task delete returns 409 while a run is live, otherwise removes the worktree.
- Local-branch deletion happens in exactly one place: `ghsync` observes
  `pr_merged`, removes any leftover worktree and force-deletes the local branch.
  Closed-unmerged PRs are left alone so a human can inspect or reopen. Remote
  branches are never deleted.

## Consequences

- Teardown and cleanup code must not delete branches elsewhere.
- All ref-mutating git calls need the per-repo lock (ADR 0009).
