# 41. Multi-repo work is a parent task with one child task per repo

Status: Accepted (design; not yet implemented)

The open questions at the end need a human decision before stage 1 ships.
AGENTS.md links to this ADR land with stage 1, since nothing is built yet.

## Context

Some work spans repos, for example an API change plus its client. Today a task
has exactly one `repo_id`, one worktree, one branch and one PR. Many invariants
rest on that:

- the dispatch lock (`active_agent_run_id` CAS per task, ADR 0001/0003) and the
  per-repo git lock (`RepoGitLock(repoPath)`, ADR 0009);
- worktrees (ADR 0008) and the `task_git` side table keyed by `task_id`, read
  through `tasks_full` (ADR 0037);
- verify (`repos.verify_command`, ADR 0038) and runtime pins (ADR 0006);
- PR creation (`internal/prflow`), auto-merge (`internal/automerge`) and
  `ghsync` `pr_merged`/`pr_closed` states;
- the diff viewer, review brief and autonomy gates (ADR 0040);
- budgets (ADR 0013).

Comparables: OpenHands multi-repository conversations (one sandbox, several
checkouts) and Devin (one session, several repos).

Subtasks exist already. `SubtasksHandler.Create`
(`backend/internal/api/handlers/subtasks.go`) makes a child that inherits
`parent.RepoID`. Depth is limited to 1, there is a per-parent cap
(`agent_configs.max_subtasks`, default 10) and a `subtasks_enabled` opt-in.
Children land on an `agent_ignore` label and the parent→child dependency edge
is created in the same transaction. `SubtaskCoordinator.OnChildTerminal`
(`internal/agent/subtasks.go`) does a `--no-ff` merge into the parent branch
(ADR 0016), sets `tasks.merge_status` (`pending`/`merged`/`merge_conflict`) and
uses `task_fanout.integration_status`. The parent auto-advances when every
child is terminal and `merge_status=merged` (`workflow.TriggerSubtasksComplete`).
`IsSubtask` makes the pool skip pushing children.

## Options considered

### 1. Parent task with one child task per repo

`create_subtask` gains an optional `repo_id`. The parent holds the cross-repo
plan and an integration step.

- Pro: every child keeps all the invariants. Verify, pins, PR and auto-merge
  work per child unchanged.
- Pro: reuses the coordinator, fan-out, dependencies and `SubtasksPanel`.
- Con: a child cannot see the sibling repo's code unless we add read-only
  context.
- Con: ADR 0016 merge-back cannot cross repos.
- Con: the parent's role changes (it may have no code of its own).

### 2. One task, several worktrees

A `task_repos` side table, extra directories mounted for the agent, one PR per
repo.

- Pro: one agent sees and edits everything, which suits tightly coupled
  changes.
- Con: breaks "one worktree/branch/PR per task" everywhere. `task_git` becomes
  1:N, so `tasks_full` and `gen.Task` change, which reopens ADR 0037.
- Con: a run must hold N repo git locks, a lock-ordering risk under ADR 0009.
- Con: verify and pins must run per directory, but providers run in a single
  `cmd.Dir`. Extra-dir support varies by provider (claude has `--add-dir`;
  see the ADR 0007 capability gaps).
- Con: the diff, review brief, autonomy, auto-merge, `ghsync` and cost must all
  become N-aware. The blast radius is large.

### 3. Linked tasks only

Cross-repo dependencies plus a shared `change_set_id`, no shared plan.

- Pro: smallest change.
- Con: no shared plan, no integration step, no single place for a human to
  approve. Ordering is manual. It is option 1 without the parent.

## Decision

Choose option 1. It keeps one repo, one worktree, one branch and one PR per
task. Option 3 is a degenerate subset of it. Option 2 is rejected for blast
radius. Its one real advantage, an agent seeing both repos, is partly recovered
by sibling read-only context (below).

Terms: a *multi-repo parent* (change set) is a parent with at least one child
where `child.repo_id != parent.repo_id`. A *foreign child* is in a different
repo from its parent; a *home child* is in the same repo.

### Schema

All additive. No `tasks` rebuild.

- A child's `tasks.repo_id` is simply another repo. No new column.
- Foreign vs home is **derived** from the repo mismatch (ADR 0027, derived over
  stored), through a helper `IsForeignChild(parent, child)`. Storing
  `task_fanout.merge_mode` (`branch`|`pr`) is the alternative (open question 2).
- Merge order is the dependency edges between children (`task_dependencies`).
  No `task_change_set` table in stage 1.
- The parent keeps a NOT NULL `repo_id`: its "primary" repo. It may have zero
  commits.

### Merge-back (ADR 0016)

Foreign children never merge into the parent branch.

- On terminal, a foreign child pushes and opens its own PR via
  `prflow.CreateForTask`, the same path as a top-level task. The `IsSubtask`
  push skip must not apply to foreign children.
- `OnChildTerminal` branches on foreign vs home before taking any lock. A
  foreign child uses `RepoGitLock` of the child's repo path, never the parent's.
  `cmd/server/main.go` currently passes the parent's `repo.Path`; stage 1 must
  resolve the child's repo path.
- `merge_status` for a foreign child follows its PR: `pending` until the PR
  exists, `pr_open`, then `merged` when `ghsync` sees `pr_merged`.
- Parent readiness (open question 1): default is every foreign child has PR
  open and verify pass. This lets the integration step review before merging.

### Dispatch and git locks (ADR 0001/0003/0009)

Unchanged per task. Each child dispatches with its own CAS. The parent is
blocked by dependency edges as today. No run holds two repo locks. The
coordinator keeps the order parent lock, then at most one repo lock. Children
in different repos run in parallel; children in the same repo serialize by
overlapping `file_scope` as today.

### How the agent sees other repos

- Writable: only its own worktree (`cmd.Dir`). The agent runs as a separate uid
  (`internal/agentuser`).
- Read-only, stage 1: prompt only. The parent plan, sibling child titles,
  descriptions and branch names, and the sibling repo map (`repomap`) text,
  fenced per ADR 0014.
- Later: an optional read-only mount of the sibling checkout, for providers
  with extra-dir support (ADR 0007). It must be read-only for the agent uid
  (r-x, not chowned). `.ate-worktrees` is writable by the agent user for its
  own worktree, so the mount needs a separate read-only bind or ACL (open
  question 5).

Prompt sketch: "You are working in repo X (writable). This change is part of
change set P, which also touches repo Y (task T, branch B). Do not edit Y.
Describe needed changes for Y in your summary, or ask via request_human."

### Verify and runtime pins

Per child, from its own repo (ADR 0038, ADR 0006), with no change. The parent's
integration run uses the primary repo's verify. A cross-repo integration test is
out of scope for stage 1 (open question 9).

### PRs, cross-linking, ordering, partial failure

- One PR per child via `prflow`. The PR body gets a "Part of change set:
  <parent title>" section listing sibling PR URLs. When a later sibling's PR
  opens, earlier siblings' PRs get an updated body or a comment (check
  `internal/forge` for an existing comment or edit call before adding one).
- Merge order: dependency edges between children, for example API before client.
- `internal/automerge` gains a block reason `change_set_order`: a child's PR is
  not auto-merged until its predecessors are `pr_merged`. Never auto-merge in a
  change set where a sibling's PR is closed-unmerged or failing checks.
- One merges, another fails: ATE never reverts merged work. The parent goes to
  needs-me with reason `change_set_partial`, showing which PRs merged and which
  did not. The human reruns the failed child through the usual feedback loop or
  closes it. This is accepted risk, mitigated by merging the backward-compatible
  side first.

### Autonomy, review brief, diff viewer

Gates evaluate per child as today (ADR 0040). No new hard stop in stage 1. A
later `cross_repo` review-brief flag ("part of a 2-repo change") can feed
`ask_on`. The diff viewer stays per task. Parent Task Detail gets a read-only
"Change set" panel: per child repo, branch, PR, verify status and diff stats,
linking to each child's diff. No merged cross-repo diff.

### Cost (ADR 0013)

Each child keeps its own `max_cost_usd` and agent-config budget. The parent
shows a roll-up (sum of children's `SumTaskCost`). An optional parent cap
checked by the pre-dispatch guard is a later stage (open question 7). Mid-run
kill stays per run.

### Limits

Depth limit 1 stays, so a foreign child cannot fan out. The cap and
`subtasks_enabled` apply unchanged. `create_subtask` validates that `repo_id`
exists, that the workflow is valid for that repo (open question 4), and that
the child lands on `agent_ignore`.

### Board and Task Detail

Foreign children show a repo chip on board cards. The parent card shows an
"N repos" badge. A parent appears under its primary repo; a foreign child
appears under its own repo with a link to the parent. `SubtasksPanel` groups
children by repo and shows PR state per child.

### Board MCP

`create_task` (`internal/boardtools/tools.go`) is unchanged and stays
single-repo. Multi-repo creation goes through planner fan-out: `create_subtask`
(HTTP body and the MCP tool in `cmd/mcp-server/main.go`) gains an optional
`repo_id`, defaulting to the parent's repo. A later optional `related_repo_ids`
hint on `create_task` can inform the planner prompt.

### Migration and back-compat

Without `repo_id`, `create_subtask` behaves exactly as today. Existing children
are all home children. No backfill, no data migration.

## Consequences

- All per-task invariants hold. New code is mostly the coordinator's foreign
  branch, prflow cross-linking, an automerge reason and UI.
- An agent cannot make an atomic cross-repo edit. Coupled changes rely on a plan
  plus ordering.
- A partial merge is possible and is handled by escalation, not rollback.
- If read-only context proves insufficient, option 2 can be revisited in a new
  ADR.

## Staged plan

Each item is one independently shippable pipeline task.

1. `create_subtask` accepts `repo_id` (HTTP, MCP tool schema, openapi) with
   validation and `IsForeignChild`. The coordinator skips `--no-ff` merge-back
   for foreign children, pushes and opens a PR on terminal, and `merge_status`
   tracks the PR. Fix `main.go` to use the child's repo path. Behind an opt-in
   (open question 3). Tests: extend `subtasks_e2e_test.go` with two temp repos.
2. Parent readiness for foreign children (PR open and verify pass); the
   integration prompt lists sibling PRs; `ghsync` `pr_merged` updates the
   child's `merge_status`.
3. Sibling prompt context: a fenced change-set section (repos, tasks, branches,
   repo-map excerpt) in child and parent prompts; the planner prompt documents
   `repo_id` and lists repos.
4. PR cross-linking: "Part of change set" body section; update sibling PRs.
5. Merge ordering in automerge (`change_set_order`) and the `change_set_partial`
   escalation.
6. UI: Task Detail change-set panel, board repo chips, `SubtasksPanel` grouped
   by repo, parent "N repos" badge.
7. Cost roll-up display and the optional parent cap in the pre-dispatch guard.
8. Review-brief `cross_repo` flag and optional autonomy `ask_on` use.
9. Optional, capability-gated: read-only sibling mount with agent-user
   read-only permissions.
10. Optional: `create_task` `related_repo_ids` hint.

## Open questions

1. Parent readiness: advance when foreign PRs are open, or merged?
2. Derive foreign vs home from the repo mismatch, or store `merge_mode`?
3. A new opt-in for cross-repo fan-out, or reuse `subtasks_enabled`?
4. Repos with different workflows: does a foreign child use the parent's
   workflow or its repo's default?
5. Read-only sibling access: prompt-only, or a mounted checkout (which ref:
   base or the sibling branch)?
6. Partial merge: escalate only (proposed), or also offer a "revert PR" helper?
7. Is a parent-level cost cap needed in v1?
8. Can a human create a multi-repo parent directly (UI or `create_task`),
   without a planner run?
9. Cross-repo integration testing (verify spanning both repos): out of scope,
   or a later stage?
