# 41. Multi-repo work is a parent task with one child task per repo

Status: Accepted (design; not yet implemented)

The nine design questions raised in the draft have been decided by the human;
see *Decided questions* at the end. The Decision section and staged plan below
reflect those answers.
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
- Foreign vs home is **decided**: derived from the repo mismatch (ADR 0027,
  derived over stored), through a helper `IsForeignChild(parent, child)`. No
  `task_fanout.merge_mode` column is stored.
- New agent-config column `agent_configs.cross_repo_subtasks_enabled INTEGER NOT
  NULL DEFAULT 0` (off by default, additive migration). It must be included in
  the agent-config version snapshot (migration 075 pattern) and exposed in the
  agent-config API and UI.
- `create_task`'s `related_repo_ids` hint is persisted in an additive side table
  `task_related_repos(task_id, repo_id, PRIMARY KEY(task_id, repo_id))`, so the
  planner prompt can read it on later runs. It is not a `tasks` column and needs
  no `tasks` rebuild.
- Merge order is the dependency edges between children (`task_dependencies`).
  No `task_change_set` table in stage 1.
- The parent keeps a NOT NULL `repo_id`: its "primary" repo. It may have zero
  commits.

### Merge-back (ADR 0016)

Foreign children never merge into the parent branch.

- On terminal, a foreign child pushes and opens its own PR via
  `prflow.CreateForTask`, the same path as a top-level task. The `IsSubtask`
  push skip must not apply to foreign children.
- `OnChildTerminal` branches on foreign vs home before taking any lock.
  `cmd/server/main.go` already passes the child's repo path to it, which equals
  the parent's today because children inherit `parent.RepoID`. The real problem
  is that `mergeChild` and `evaluateParent` (and `AfterParentRun`, which gets the
  parent's path and runs child worktree/branch cleanup with it) reuse that single
  `repoPath` for both child and parent-branch operations. Stage 1 must split
  child and parent repo paths. Child worktree/branch operations use the child
  repo's `RepoGitLock`. Parent-branch operations use the parent repo's path and
  lock. A foreign child never touches the parent's repo or lock, and
  `AfterParentRun` skips foreign children (they have no branch to merge back).
- `merge_status` for a foreign child follows its PR: `pending` until the PR
  exists, `pr_open`, then `merged` when `ghsync` sees `pr_merged`.
- Parent readiness is **decided**: the parent advances
  (`workflow.TriggerSubtasksComplete`) when every foreign child's PR is open and
  its verify passed. Merged is not required. Home children keep today's rule
  (`merge_status=merged`). The integration step reviews before any merge.

### Dispatch and git locks (ADR 0001/0003/0009)

Unchanged per task. Each child dispatches with its own CAS. The parent is
blocked by dependency edges as today. No run holds two repo locks. The
coordinator keeps the order parent lock, then at most one repo lock. Children
in different repos run in parallel; children in the same repo serialize by
overlapping `file_scope` as today.

### How the agent sees other repos

- Writable: only its own worktree (`cmd.Dir`). The agent runs as a separate uid
  (`internal/agentuser`).
- Read-only in v1 (**decided**): prompt only. The parent plan, sibling child titles,
  descriptions and branch names, and the sibling repo map (`repomap`) text,
  fenced per ADR 0014.
- Later, optional: a read-only mount of the sibling checkout, for providers
  with extra-dir support (ADR 0007). It must be read-only for the agent uid
  (r-x, not chowned). `.ate-worktrees` is writable by the agent user for its
  own worktree, so the mount needs a separate read-only bind or ACL. Which ref
  to mount (base or the sibling branch) is decided when that stage is designed.

Prompt sketch: "You are working in repo X (writable). This change is part of
change set P, which also touches repo Y (task T, branch B). Do not edit Y.
Describe needed changes for Y in your summary, or ask via request_human."

### Verify and runtime pins

Per child, from its own repo (ADR 0038, ADR 0006), with no change. The parent's
integration run uses the primary repo's verify. A cross-repo integration test (a
verify spanning both repos) is out of scope for v1 and listed under later
stages.

### PRs, cross-linking, ordering, partial failure

- One PR per child via `prflow`. The PR body gets a "Part of change set:
  <parent title>" section listing sibling PR URLs. When a later sibling's PR
  opens, earlier siblings' PRs get an updated body or a comment (check
  `internal/forge` for an existing comment or edit call before adding one).
- Merge order: dependency edges between children, for example API before client.
- `internal/automerge` gains a block reason `change_set_order`: a child's PR is
  not auto-merged until its predecessors are `pr_merged`. Never auto-merge in a
  change set where a sibling's PR is closed-unmerged or failing checks.
- One merges, another fails: ATE never reverts merged work, and there is no
  revert-PR helper (**decided**: escalation only). The parent goes to
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
shows a roll-up (sum of children's `SumTaskCost`, plus the parent's own
runs). **Decided:** no parent-level cost cap in v1. A parent cap checked by the
pre-dispatch guard is a later stage. Mid-run kill stays per run.

### Limits

- Depth limit 1 stays, so a foreign child cannot fan out. The per-parent cap
  stays.
- A foreign `repo_id` on `create_subtask` additionally requires the creating
  run's agent config to have `cross_repo_subtasks_enabled`. `subtasks_enabled`
  alone permits only home children; otherwise 403 "this agent config is not
  permitted to create cross-repo subtasks".
- Workflow (**decided**): a foreign child uses the parent's workflow
  (`workflow_id = parent.WorkflowID`). Its landing label is resolved from that
  workflow (must be `agent_ignore`) exactly as today. The foreign repo's default
  workflow is ignored for children. Verify and pins still come from the child's
  own repo. `create_subtask` validates that `repo_id` exists.

### Board and Task Detail

Foreign children show a repo chip on board cards. The parent card shows an
"N repos" badge. A parent appears under its primary repo; a foreign child
appears under its own repo with a link to the parent. `SubtasksPanel` groups
children by repo and shows PR state per child.

### Creating a multi-repo task (planner, human, REST/MCP)

- **Planner fan-out**: `create_subtask` (HTTP body, openapi, MCP tool in
  `cmd/mcp-server/main.go`) gains an optional `repo_id`, defaulting to the
  parent's repo.
- **Direct creation, v1** (changed from the draft): REST `POST /api/tasks` and
  MCP `create_task` (`internal/boardtools/tools.go`) gain two optional fields:
  - `related_repo_ids: [repo_id]`. Stored in `task_related_repos`. The planner
    prompt says "this change spans repos X, Y; create one child per repo (via
    `create_subtask` with `repo_id`)". The children are still created by the
    planner's `create_subtask` calls, so the planner run's config opt-in also
    applies at fan-out time.
  - `children: [{repo_id, title, description}]`. Created in the **same
    transaction** as the parent, each with `parent_task_id` = the new task, the
    parent→child dependency edge, the parent's workflow and the parent
    workflow's `agent_ignore` gate label. This is the same as `create_subtask`
    output, so the coordinator treats them identically. Created children have no
    creating run (`created_by_run` NULL).
  - Both may be given together. Related repos without an explicit child are left
    to the planner.
  - Multipart requests carry `related_repo_ids` as repeated form fields and
    `children` as a JSON-encoded form field. JSON requests carry them natively.
- **Validation** (400/403/422, nothing written on failure):
  - every repo id exists;
  - `related_repo_ids` are distinct and exclude the task's own `repo_id`;
  - each child has a non-empty title and an existing `repo_id`;
  - number of children is at most the per-parent cap (`defaultSubtaskCap`, 10,
    since there is no creating run's config);
  - depth limit 1: the new task is top-level, and children cannot carry nested
    `children` or a parent;
  - the opt-in: when any related repo or child repo differs from the task's
    repo, at least one enabled agent config that serves a label in the task's
    workflow must have `cross_repo_subtasks_enabled`. Otherwise 403, so the
    change set cannot stall with no agent allowed to handle it. This is the
    chosen interpretation of "the opt-in" for human and MCP creation.
- **New Task dialog** (`NewTaskModal.tsx`): an optional "Also touches"
  multi-select repo picker, listing configured repos except the selected primary
  repo and shown only when more than one repo exists. It sends
  `related_repo_ids`. An optional "Create child tasks now" toggle expands one
  title/description row per picked repo and sends `children` instead. Server
  errors (opt-in missing, cap) are shown inline.
- **Back-compat**: both fields are optional. Without them `create_task` behaves
  exactly as today (single repo, no side-table rows). Existing MCP clients and
  the stdio tool list are unaffected.

### Migration and back-compat

Without `repo_id`, `create_subtask` behaves exactly as today. Existing children
are all home children. `cross_repo_subtasks_enabled` defaults to 0, so existing
configs cannot fan out across repos. `task_related_repos` starts empty.
`create_task` without the new fields is unchanged. No backfill, no data
migration.

## Consequences

- All per-task invariants hold. New code is mostly the coordinator's foreign
  branch, prflow cross-linking, an automerge reason and UI.
- An agent cannot make an atomic cross-repo edit. Coupled changes rely on a plan
  plus ordering.
- A partial merge is possible and is handled by escalation only, not rollback
  and not a revert helper.
- A human can start a change set directly, without a planner deciding to fan out.
- If read-only context proves insufficient, option 2 can be revisited in a new
  ADR.

## Staged plan

Each item is one independently shippable pipeline task.

v1, in order:

1. `create_subtask` accepts `repo_id` (HTTP, MCP tool schema, openapi) with
   validation, `IsForeignChild`, the `cross_repo_subtasks_enabled` opt-in
   (migration, config version snapshot, config UI) and parent workflow
   inheritance. Split child and parent repo paths in `OnChildTerminal`,
   `mergeChild`, `evaluateParent` and `AfterParentRun`. The coordinator skips
   `--no-ff` merge-back for foreign children; a foreign child pushes and opens
   its own PR on terminal (no `IsSubtask` push skip), and `merge_status` tracks
   the PR. Tests: extend `subtasks_e2e_test.go` with two temp repos.
2. Parent readiness for foreign children: PR open and verify passed, not
   merged. `ghsync` `pr_merged` updates the child's `merge_status`. The
   integration prompt lists sibling PRs.
3. Sibling prompt context: a fenced change-set section (repos, tasks, branches,
   repo-map excerpt) in child and parent prompts. The planner prompt documents
   `repo_id`, lists repos, and includes `related_repo_ids` when set.
4. Human and MCP creation of multi-repo tasks: `create_task`
   `related_repo_ids` + `children` (REST incl. multipart, MCP tool, openapi),
   `task_related_repos`, same-transaction child creation, validation, and the
   New Task dialog's "Also touches" picker.
5. PR cross-linking: a "Part of change set" body section, plus updating sibling
   PRs.
6. Merge ordering in automerge (`change_set_order`) and `change_set_partial`
   escalation to needs-me (no revert helper).
7. UI: Task Detail change-set panel, board repo chips, `SubtasksPanel` grouped
   by repo, parent "N repos" badge, rolled-up cost on the parent.

Later:

- Parent-level cost cap in the pre-dispatch guard.
- Review-brief `cross_repo` flag and optional autonomy `ask_on` use.
- Read-only sibling mount (capability-gated, agent-user read-only; ref to be
  decided).
- Cross-repo integration tests (verify spanning repos).

## Decided questions

1. Parent readiness: advance when foreign PRs are open, or merged?
   **Decided:** open and verify passed, not merged.
2. Derive foreign vs home from the repo mismatch, or store `merge_mode`?
   **Decided:** derived from the repo mismatch; no stored `merge_mode`.
3. A new opt-in for cross-repo fan-out, or reuse `subtasks_enabled`?
   **Decided:** new `cross_repo_subtasks_enabled`, off by default;
   `subtasks_enabled` alone does not allow cross-repo fan-out.
4. Repos with different workflows: does a foreign child use the parent's
   workflow or its repo's default? **Decided:** the parent's workflow.
5. Read-only sibling access: prompt-only, or a mounted checkout (which ref:
   base or the sibling branch)? **Decided:** prompt-only in v1; the read-only
   mount is a later optional stage.
6. Partial merge: escalate only, or also offer a "revert PR" helper?
   **Decided:** escalate to needs-me only; no revert helper.
7. Is a parent-level cost cap needed in v1? **Decided:** no cap in v1; show the
   rolled-up cost on the parent.
8. Can a human create a multi-repo parent directly (UI or `create_task`),
   without a planner run? **Decided (changed from draft):** yes. The New Task
   dialog and REST/MCP `create_task` can create one directly in v1 via
   `related_repo_ids` and/or explicit `children`; see "Creating a multi-repo
   task".
9. Cross-repo integration testing (verify spanning both repos): out of scope,
   or a later stage? **Decided:** out of scope for v1; listed under later
   stages.
