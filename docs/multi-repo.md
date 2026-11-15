# Multi-repo tasks

A change that spans several repos is modelled as a **change set**: one parent task in the primary repo plus one child task per other repo, each with its own branch, verify run and pull request. See [ADR 0041](adr/0041-multi-repo-work-as-parent-with-per-repo-children.md) for the design and its limits.

## Creating one

- **New Task dialog:** pick extra repos under "Also touches".
- **REST:** `POST /tasks` with `related_repo_ids` or `children` (see [api.md](api.md)).
- **Planner:** `create_subtask` with `repo_id`, allowed when the agent config has `cross_repo_subtasks_enabled` or the repo was named on the task (see [agents.md](agents.md)).

## What happens per child

Each child (a *foreign child* when its repo differs from the parent's) works on its own branch, is verified like any task, and lands through its own PR instead of merging back into the parent. PRs of a change set cross-link each other. Children use the parent's workflow. Task-source write-back is described in [task-sources.md](task-sources.md).

## Merge order and auto-merge

Dependency edges between children define the merge order. With auto-merge on, a PR waits (`change_set_order`) until the PRs it depends on merged, and holds when a sibling PR is closed or failing (`change_set_sibling_failed`). If part of a change set merges and another PR is closed or cannot be opened, the parent raises the needs-you reason `change_set_partial`. ATE never reverts merged work: rerun the failed child or archive it.

## In the UI

- **Task Detail on the parent:** a "Change set" panel groups the children by repo (primary repo first). Each row shows the task link, label, branch, PR link and state, verify result, diff stats, auto-merge state or block reason and merge position, plus a link to that child's own diff. There is no merged cross-repo diff.
- **Task Detail on a child:** a "Part of change set" line links the parent and the siblings.
- **Board:** a repo chip on foreign-child cards and an "N repos" badge on the parent card. The repo filter shows a parent under its primary repo and each foreign child under its own repo.
- **Cost:** the parent shows `$X total (change set) · $Y this task`, where the total is its own cost plus all children's runs (`rolled_up_cost_usd`, computed on read). There is no parent-level cost cap in v1; each child keeps its own `max_cost_usd`.
- **Needs you / review popup:** `change_set_partial` lists the merged and not-merged PRs, each linking to its child task.

## Limits

Depth is 1 (a child cannot have children), there is no cross-repo diff, and siblings only see each other through the prompt (no shared mounts).
