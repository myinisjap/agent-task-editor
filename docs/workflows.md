# Workflows

A workflow is the state machine that governs how tasks progress. Each workflow contains **labels** (columns) and **transitions** (allowed moves between labels).

> A condensed version of this reference is also available in the app: click the ⓘ button in the Workflow page header (Pipeline → Workflow tab).

## Labels

| Field | Type | Description |
|---|---|---|
| `name` | string | Unique identifier within the workflow (used as the task's current state) |
| `color` | string | Hex color for UI display. Must be empty (falls back to the UI default) or a hex literal matching `^#[0-9a-fA-F]{3,8}$` (`#rgb`, `#rrggbb`, or `#rrggbbaa`) — anything else is rejected with 400 on save/import. This value is interpolated into SVG markup by the dashboard's factory visualization, so it is validated server-side to prevent HTML/CSS injection (see #343). |
| `sort_order` | int | Column order on the board |
| `agent_ignore` | bool | Agents cannot move tasks to this label; dispatcher skips tasks already here |
| `is_terminal` | bool | No further transitions; task is complete |
| `create_pr` | bool | Any transition into this label pushes the task's branch and auto-opens (or reuses) a PR on the repo's forge (GitHub via the REST API, or Gitea). Requires a repo `remote_url` on a supported forge and working push/auth; best-effort, so a failure is logged and the transition still commits. Equivalent to clicking "Create PR" manually. The seeded Default workflow sets it on `review`. **At most one label per workflow may set this** — saving a workflow with two is rejected. |
| `wip_limit` | int, nullable | Max number of (non-archived) tasks allowed on this label. `null`/unset = unlimited — a label with no limit behaves exactly as before this feature existed. Must be a positive integer when set; `0` or negative is rejected. |
| `wip_limit_hard` | bool | Enforcement mode for `wip_limit`. **Soft (default, `false`)**: the board column header shows `count / limit` and flags the column once its count reaches or exceeds the limit, but nothing is blocked. **Hard (`true`)**: in addition to the visual flag, the dispatcher will not pick up a task whose agent "success" transition would land it on this label while the label's count is already at or over its limit — the task simply stays where it is and is re-evaluated on the next sweep (a few seconds later), so work queues upstream instead of erroring mid-run. Hard mode never blocks a run that is already in flight from landing in a full column; that stays visual-only. Setting `wip_limit_hard` without a `wip_limit` has no effect (there's nothing to enforce). |
| `verify_mode` | `inherit` \| `custom` \| `disabled` | Per-label override of the repo's [verify command](#verify-commands). `inherit` (default, may be omitted) uses the repo command; `custom` runs this label's `verify_command` instead; `disabled` skips verification for runs that succeed on this label. |
| `verify_command` | string | The label's own command. Required when `verify_mode` is `custom`; ignored (and cleared) otherwise. The repo's `verify_timeout_seconds` applies. |

## Pipeline overview

The **Pipeline** page (`/pipeline`, Overview tab) draws the selected workflow as a graph: labels are nodes, transitions are edges (green success, red failure, dashed for agent-triggered). Each node shows who works it: the enabled agent config(s) whose `labels` include it, **Human gate** for labels only a human can move, or an amber **No agent assigned** chip for agent-triggerable labels nobody covers. Nodes also show a non-default verify mode, `terminal`, and `opens PR` markers. The Board shows the same chip in the header of unstaffed columns.

Click a node to open the assign panel: tick or untick an agent config to add or remove that label from its `labels` (the same update the Agents tab performs). Use **Edit workflow** (see below) or the Workflow tab to change the labels and transitions themselves. With several workflows, a selector above the graph switches between them.

## Editing the workflow graph

The Pipeline Overview graph has an **Edit workflow** button. Edits are staged locally (an "Unsaved changes" indicator shows, with **Save** and **Discard** buttons) and nothing is written until you save. The YAML editor on the Workflow tab stays available as the Advanced view; both write the same model.

Supported edits:

- **Add a label** (name and color) from the side panel; **rename** or **delete** the selected label. Renaming rewrites its transitions; deleting also removes the transitions touching it. Labels that currently have tasks on them cannot be renamed or deleted (tasks reference labels by name).
- **Connect two labels**: drag from a stage's bottom dot to another stage's top dot (creates an agent-on-success transition), or use the **Connect labels** form (from, to, type) for keyboard use. Each transition in the **Transitions** list has a type selector (agent on success, agent on failure, agent either outcome, human, agent or human) and a delete button.
- **Per-label settings** (select a stage): whether agents work it (`agent_ignore`), **terminal**, **open a PR on entry** (`create_pr`, at most one label), **WIP limit** (and hard limit), and **verify mode** (inherit / custom with a command / disabled).
- **Human approval gate**: the "Human approval gate" checkbox makes every non-failure transition out of the label human-triggered (and turns them back into agent-on-success when unticked).

Validation mirrors the server: unique non-empty names, hex colors, positive WIP limits, one `create_pr` label, a command for custom verify, transitions that reference existing labels, and no transitions out of terminal labels. Errors block Save; unreachable labels only warn. Server errors show inline.

Saving uses `PUT /workflows/{id}/yaml` (the only write path that round-trips `create_pr`, WIP and verify settings together), which, like the YAML tab, does not keep a per-transition `agent_config_id`; assign agents to labels instead. The API has no ETag, so Save re-reads the workflow first and, if it changed on the server since you started editing (different `updated_at` or content), shows a conflict banner with **Reload and discard my edits** or **Overwrite with my edits** rather than silently overwriting.

## Verify commands

An agent's `signal_complete(outcome="success")` (or the parsed `OUTCOME: success` marker) is only a *claim*. A repo can configure an independent check that decides whether that claim is believed.

- **Repo config** (Repos page, or `verify_command` / `verify_timeout_seconds` on `POST`/`PATCH /repos`): a shell command such as `go test ./...` or `npm test`, and a timeout (1-3600 s, default 600). Empty command = no verification; behaviour is exactly as before.
- **Per-label override** (workflow YAML or the `labels` array of `PUT /workflows/{id}`): `verify_mode: inherit | custom | disabled` plus `verify_command` (see the label fields above). The label that applies is the one the task was on when the run succeeded.
- **When it runs**: after the run is recorded as completed and the safety-net commit has captured the final tree, but **before** the success transition. It runs with `sh -c` in the task's worktree (falling back to the repo path), as the unprivileged agent user with the allow-listed agent environment and the repo's `mise`/`uv` runtime pins ([runtime.md](runtime.md)), in its own process group that is killed on timeout. Output (stdout and stderr combined) is capped at 16 KiB, keeping the tail.
- **Authoritative**: exit 0 lets the success transition proceed. A non-zero exit or a timeout overrides the agent's self-report. The success transition is **not** taken: if the label has an agent failure path (e.g. `testing` to `work`) it fires with a note like `Verification failed: command exited with code 1`; if not, the task keeps its label, is unlocked, and the attempt counts toward the consecutive-failure limit (3), after which the run escalates to `waiting_human`. The failure path is also subject to the usual rework-loop circuit breaker.
- **Feedback**: the next run's prompt starts with a delimited section, `VERIFICATION FAILED:` (command, exit code or timeout, then the output between `--- BEGIN VERIFY OUTPUT ---` and `--- END VERIFY OUTPUT ---` fences, treated as untrusted). The agent sees the real error, not its own recollection.
- **Could not run** (no verifier wired, spawn failure, missing directory): the status is `error`, the run is escalated to `waiting_human` and the task stays on its label; nothing is sent back to the agent.
- **Record**: one `RunVerification` per run (`status` `pass | fail | timeout | error`, `exit_code`, output tail, `duration_ms`, the `command` used) via `GET /tasks/{id}/verifications`, a system line in the run log, and a `task.verification` WebSocket event. The Diff tab of the task page shows the latest result (badge, command, collapsible output) above the diff.

Only `success` outcomes are verified; a run that reports `failure` is not.

The latest result is also summarised in the [Review brief](#review-brief) shown while a task waits for approval.

## WIP limits and the dispatcher

Hard WIP limits are enforced at exactly one point: the dispatcher's sweep, right before it would start a new agent run for a task. Before dispatching, it resolves the task's current label's unambiguous agent-triggerable "success" transition target. If that target label has `wip_limit_hard: true` and its current occupancy (non-archived tasks currently on that label, matching what the board column count shows) is `>= wip_limit`, the dispatcher skips this task for the current sweep — no run is started, no error is recorded, and the task's label doesn't change. It's picked up again on a later sweep once the target label has room.

If the current label has no single unambiguous "success" transition (e.g. none defined, or more than one agent transition could plausibly be "success"), the WIP check is skipped entirely for that task — an ambiguous target is treated as "unknown" rather than guessing, so it never blocks dispatch.

## Transitions

Each transition defines an allowed `from → to` move:

| Field | Description |
|---|---|
| `from_label` | Source label name |
| `to_label` | Destination label name |
| `trigger_type` | `agent`, `human`, or `both` |
| `path` | `success` or `failure` — which outcome this transition represents (used by Approve/Reject) |

A task can only move to labels that have a matching transition from its current label. The workflow engine enforces this — any attempt to move outside the defined transitions returns `ErrNoTransition`.

## Default Workflow

Seeded automatically on first startup:

```
not_ready ──(human)──▶ plan ──(agent, success)──▶ review-plan
                                                        │
                                    ┌───────────────────┴───────────────────┐
                                    │ (human, failure)                      │ (human, success)
                                    ▼                                       ▼
                                  plan                                    work
                                                                            │
                                                                       (agent, success)
                                                                            ▼
                                                                         testing
                                                                            │
                                              ┌─────────────────────────────┤
                                              │ (agent, failure)            │ (agent, success)
                                              ▼                             ▼
                                             work                    agent-review
                                                                            │
                                              ┌─────────────────────────────┤
                                              │ (agent, failure)            │ (agent, success)
                                              ▼                             ▼
                                             work                        review
                                                                            │
                                              ┌─────────────────────────────┤
                                              │ (human, failure)            │ (human, success)
                                              ▼                             ▼
                                             work                         done
```

`not_ready` has no incoming transitions in the default workflow — tasks start there and move forward via `not_ready → plan`, but nothing routes back to it automatically.

**Label flags in the default workflow:**
- `not_ready` — `agent_ignore = true` (nothing runs here)
- `done` — `is_terminal = true`

## Approve and Reject

The `/tasks/{id}/approve` and `/tasks/{id}/reject` endpoints move tasks along human-gated transitions.

**Approve** (`POST /tasks/{id}/approve`) — follows the `success` human transition defined for the current label. Optional body:

```json
{ "note": "optional note recorded in label history" }
```

**Reject** (`POST /tasks/{id}/reject`) — follows the `failure` human transition defined for the current label. The `to_label` field overrides the auto-resolved target. The `note` is stored as feedback on the prior agent run and injected at the top of the next run's prompt under `"FEEDBACK FROM PRIOR REVIEW:"`.

```json
{
  "note": "Please fix the edge cases in the payment flow",
  "to_label": "optional override label"
}
```

If no matching transition is defined for the current label, the endpoint returns `400`.

## Review brief

When a task sits on a **human-gate** label (no agent-triggerable way out and at least one human transition, the same rule as `GET /needs-me` `human_gate`; `agent_ignore` backlog labels excluded), the Task Detail page shows a compact **Review brief** at the top of the Overview tab and above the diff on the Diff tab, and the Inbox shows the flag chips on the row. It is computed from existing data with **no LLM calls**, and it is **advisory only**: nothing is ever approved or blocked because of a flag.

The brief is served by `GET /tasks/{id}/review-brief`; the Inbox gets flag codes only (`risk_flags` on `/needs-me` items, human-gate items only).

**Risk flags** (computed from the diff of the task's branch against its merge-base, the same diff as the Diff tab; rules live in `backend/internal/reviewbrief/flags.go`). Flags always come out in this order:

| Code | Fires when |
|---|---|
| `migration` | a path segment is `migrations`; or a `.sql` file whose name contains `schema`; or a `.sql` file with an added/removed `CREATE\|ALTER\|DROP TABLE\|INDEX\|VIEW\|TRIGGER` line. Plain query files (SELECT/INSERT only) do not fire. |
| `auth_or_security` | the path contains `auth`, `oauth`, `token`, `secret`, `middleware`, `crypto`, `password`, `credential`, `jwt`, `csrf`, `cors`, `permission`, `security` or `session`, or the file is `.env*`. Substring match, so e.g. `tokenizer.go` is a known false positive. |
| `dependencies` | `go.mod/sum`, `package.json` and npm/yarn/pnpm/bun lockfiles, `Cargo.*`, `Pipfile*`, `poetry.lock`, `pyproject.toml`, `uv.lock`, `Gemfile*`, `requirements*.txt` (or `.txt` under `requirements/`), or a Dockerfile with an added/removed `FROM` line. |
| `tests_removed` | a test file was deleted (or renamed to a non-test path); or across test files more test declarations were removed than added (`func Test…`, `it/test/describe(`, `def test_`); or an added line skips/focuses tests (`t.Skip`, `it.skip`/`.only`, `xit`, `fit`, `@pytest.mark.skip`). |
| `ci_config` | `.github/workflows/**`, `.gitea/workflows/**`, `.gitlab-ci.yml`. |
| `large_change` | more than 800 changed lines or more than 25 files. Lockfiles and generated files (`frontend/src/api/types.ts`) are excluded from the line count. |
| `outside_plan_scope` | the plan in the task's `agent_notes` lists file paths (backtick spans or list items containing path-like tokens) and the diff touches other files. Skipped when the plan has no file list. Exempt: `CHANGELOG.md`, the test sibling of a planned file, `frontend/src/api/types.ts` when `openapi.yaml` is planned, `backend/internal/storage/gen/**` when a planned path is under `storage/queries/` or `storage/migrations/`, `go.sum` when `go.mod` is planned, lockfiles when `package.json` is planned. It is a heuristic and prefers under-flagging. |

**Verified** shows the latest verify-command result (status, duration, output tail; see [Verify commands](#verify-commands)), plus the run count (and failures), rework loops (transitions into a label the task already visited, the same definition as the Dashboard's outcome-quality rework rate) and cost so far.

**Agent summary** is the newest run's `store_info` (first 1500 characters), falling back to the tail of the task's `agent_notes`. Reviewer agents are best prompted to end with a fixed-shape `store_info`: **Changed / Risks / Verified / Not checked**.

**Caching**: the diff-derived part is cached in memory per task, keyed on the branch head, base ref head and a hash of `agent_notes`; a cached entry checked in the last 15 seconds is reused without running git. Nothing is stored on the task.

### Preview screenshots

For UI changes, the review brief also shows screenshots of the affected pages (`GET /tasks/{id}/previews`), so a change can be reviewed from a phone. Everything is per repo and **off unless a preview command is set**:

| Setting | Meaning |
|---|---|
| `preview_command` | Shell command that starts the app in the task's worktree, e.g. `cd frontend && npm ci && npm run dev -- --port $PORT --host 127.0.0.1`. It runs as the agent user with the same sanitized environment as the verify command, and must listen on `$PORT` (also set: `HOST=127.0.0.1`). |
| `preview_routes` | Newline-separated paths to screenshot (`/`, `/inbox`, ...), at most 20. |
| `preview_paths_glob` | Newline-separated globs deciding when a diff is a UI change. Empty = `frontend/**`, `web/**`, `src/**/*.{tsx,jsx,vue,svelte,css}`. `{a,b}` alternation is supported. |

**When it runs.** After a run reports success (and any verify command passed) and the task lands on a **human-gate** label, if a preview command and routes are set and at least one file changed against the base branch matches the globs. Capture happens asynchronously after the transition, so it never delays the task or holds a worker slot. The app is started with a free `$PORT`, the server waits up to 120 s for the port, each route is captured with headless Chromium at **1280x800 (desktop)** and **390x844 (phone)**, then the process group is stopped (SIGTERM, then SIGKILL after 5 s). A whole capture is limited to 5 minutes.

**Storage.** PNGs are stored in `UPLOAD_DIR/<task_id>/preview-<uuid>.png` and listed in `task_preview_images`; they are *not* task attachments, so they are never fed to agents. The latest 3 captures per task are kept; deleting the task removes the files. Screenshots appear in the review brief card (Task Detail and Inbox) as a thumbnail strip that opens full size.

**Failures never block.** Any problem (command exits, port never opens, browser missing, one route failing) is recorded on the capture and shown as "Preview failed: ..." in the brief; the task label, failure counts and run status are untouched. A capture left `running` by a server restart is marked failed at startup.

**v1 limits.** After-only: there is no base-branch "before" shot yet (the `variant` column leaves room for it). Dev servers that daemonize into a new session escape the process-group cleanup. See [runtime.md](runtime.md#preview-screenshots) for the browser setup.

## Custom Workflows

Create additional workflows via Pipeline → Workflow → New Workflow in the UI, or via the REST API:

```http
POST /api/v1/workflows
{ "name": "My Workflow", "description": "..." }
```

### Import / Export

Workflows can be exported as YAML for version control and imported into another instance:

```bash
# Export
GET /api/v1/workflows/{id}/export.yaml

# Import
POST /api/v1/workflows/import
Content-Type: application/yaml
<yaml body>
```

YAML format:

```yaml
name: My Workflow
description: Optional description
labels:
  - name: backlog
    color: "#6B7280"
    sort_order: 0
    agent_ignore: true
  - name: in-progress
    color: "#F59E0B"
    sort_order: 1
  - name: review
    color: "#EC4899"
    sort_order: 2
    wip_limit: 5        # column shows "count / 5" and flags over-limit
    wip_limit_hard: true # dispatcher also stops routing new work here once full
  - name: done
    color: "#10B981"
    sort_order: 3
    is_terminal: true
transitions:
  - from: backlog
    to: in-progress
    trigger: human
  - from: in-progress
    to: done
    trigger: agent
  - from: done
    to: in-progress
    trigger: human
```

## Workflow Engine Rules

1. A transition must exist in `workflow_transitions` for `(workflow_id, from_label, to_label)`.
2. If the transition has `trigger_type = human`, only human-triggered calls succeed; agent calls receive `ErrGateRequired`.
3. If the destination label has `agent_ignore = true`, agents cannot move tasks there (`ErrAgentIgnored`).
4. Every successful transition is recorded in `task_label_history` with the trigger, actor ID, and optional note.
5. Whenever `UpdateTaskLabel` runs (any transition), `active_agent_run_id` is automatically cleared — preventing stale dispatch locks.

## Force move (escape hatch)

Nothing in the default workflow routes back to `not_ready`, so a task wedged in `plan` or `work` could otherwise not be parked. A human can **force move** a task to any label of its workflow, bypassing the transition table, via `PATCH /tasks/{id}/label` with `"force": true` or the "Force move…" button on Task Detail (which opens a confirmation dialog with a warning and an optional note).

- The target must be a label of the task's workflow (else `400`).
- Refused with `409` while an agent run on the task is `pending`/`running`; cancel the run first. Runs are never cancelled silently.
- It goes through the engine's `ForceTransition`, so everything that normally happens on entering a label still does: `active_agent_run_id` is cleared, `task.label_changed` is published, and terminal / create-PR / leave-`not_ready` hooks fire.
- Label history records it with trigger `human`, the actor, and a note starting with `Forced move`.

## Task Dependencies

A task can declare **dependencies** on other tasks in the same workflow — a way
to express "don't dispatch B until A is done" so multi-task work can be queued
without babysitting the board.

- **Dispatch-only gate.** A task with at least one *unsatisfied* blocker is never
  picked up by the dispatcher. It is not paused, archived, or moved — it simply
  sits on its current label until its blockers finish. Humans can still drag it
  anywhere; dragging a blocked task into an agent-triggerable column pops a
  confirmation, and the task stays un-dispatched (and visibly muted with a
  "blocked by N" badge) until the block clears.
- **Satisfied = terminal or archived.** A blocker satisfies its edge once it
  reaches a label with `is_terminal = true`, **or** is archived. (A blocker that
  is a subtask must also have finished merging back; see [Subtasks](#subtasks-agent-driven-decomposition).) Archiving is the
  existing "this is over" gesture; treating an archived blocker as forever-blocking
  would create an invisible deadlock. (Un-archiving makes the edge unmet again.)
- **Derived, never stored.** "Blocked" is computed at read time from the blocker's
  current state — there is no status column to drift and no event required when a
  blocker completes. The next dispatch sweep just sees the task as eligible.
- **No cycles, no self-edges, no cross-workflow edges.** These are rejected when
  the edge is created. A blocker whose workflow has no terminal label is also
  rejected, since such an edge could never satisfy.
- **Not parent/child.** A dependency is a peer relationship with no automation: a
  dependent task always has its own work, and simply becomes dispatch-eligible on
  its current label once its blockers finish. (Agent-driven parent/child decomposition is covered under
  [Subtasks](#subtasks-agent-driven-decomposition).)

Manage dependencies from a task's detail page (the Dependencies section) or via
`GET/POST /tasks/{id}/dependencies` and `DELETE /tasks/{id}/dependencies/{dep_id}`.
Task list/detail responses carry derived `blocked_by_count` and `blocking_count`.

## Subtasks (agent-driven decomposition)

A planning agent can split a large task into structured child tasks instead of
leaving prose in `agent_notes`. Subtasks build on the dependency gate above.

- **Opt-in per agent config.** Only a config with `subtasks_enabled` (off by
  default) exposes the `create_subtask` MCP tool; `max_subtasks` (default 10)
  caps children per parent. Typically only the planner gets this. The
  `create_subtask` tool writes live to `POST /tasks/{id}/subtasks`, so children
  appear on the board mid-run.
- **Human gate.** Children land on the workflow's first `agent_ignore` label
  (the seed workflow: `not_ready`) so a human sanity-checks the decomposition
  before agents fan out, then releases them (bulk-move out of the gate).
- **Relationship.** `parent_task_id` groups a child under its parent (rollup,
  provenance via `created_by_run_id`); an auto-created **parent→child dependency
  edge** is the dispatch gate, so the parent isn't dispatched until every child
  reaches a terminal label. Depth is limited to 1 — a subtask can't create
  subtasks.
- **Branch off parent, merge back.** A child's worktree is cut from the parent's
  branch. When a child reaches a terminal label its branch is merged back into
  the parent's branch (a plain merge commit); the child's worktree/branch are
  then removed. Children never push to origin or open PRs — the parent's branch
  (and its single eventual PR) is the only outward-facing artifact, and
  `GET /tasks/{parent}/diff` shows the integrated result.
- **Conflicts.** A conflicting merge-back is aborted cleanly and the child is
  flagged `merge_conflict`. Because the child is terminal, the parent's edge is
  satisfied and the parent becomes dispatch-eligible; the dispatcher hands the
  parent's `work` agent the conflict context to resolve the merge on the parent
  branch. A human can also resolve it manually.
- **File scopes and the overlap guard.** `create_subtask` takes an optional
  `file_scope` (repo-relative globs, e.g. `backend/internal/foo/**`). The scope
  is stored and shown in the subtask's prompt ("only change files matching …; if
  you must go outside it, explain in your notes"). After the subtask's work run a
  deterministic check lists any changed files outside the scope in the subtask's
  notes under `FILE SCOPE WARNING` (a warning, never a failure). When a new
  subtask's scope **overlaps** a sibling's, it automatically **depends on that
  sibling**, so the two run sequentially, and the tool result says so. Overlap is
  deliberately conservative (a false positive only serializes two subtasks): equal
  globs, a literal file inside the other glob, or a shared literal directory
  prefix. A basename-only pattern such as `*.go` overlaps everything, so use
  directory-rooted globs. A subtask with **no** scope is treated as unknown and
  gets no overlap check; planners should give every subtask a scope. Finished
  (terminal) and archived siblings are skipped.
- **Stricter "satisfied" for subtask blockers.** A blocker that is itself a
  subtask satisfies an edge only once it is archived, or is on a terminal label
  **and** its merge-back has finished (`merge_status` `merged` or
  `merge_conflict`). This closes a race where a parent (or an overlap-serialized
  sibling) could be dispatched after the child went terminal but before its
  branch was merged, so it never saw the child's work. It applies to every edge
  whose blocker is a subtask, including human-added ones.
- **How branches combine.** Each subtask branch is merged into the parent branch
  as soon as that subtask reaches a terminal label — one merge at a time, in
  completion order, under a per-parent lock. A conflicting merge is aborted, the
  child is flagged `merge_conflict`, and resolving it goes to the parent's work
  agent during the integration run.
- **Integration run (replaces auto-advance).** Once every non-archived child is
  terminal and merged (or conflicted), the parent is marked
  `integration_status = pending` and is **not** auto-advanced. The dispatcher runs
  the parent's agent on its current label (normally `work`) with a **SUBTASK
  INTEGRATION** prompt section: each subtask with its label, scope, branch and
  notes, the `git diff --stat` of the merged result, any conflicts to resolve, and
  instructions to check cross-subtask consistency and run the repo verify command.
  The usual verify command runs again after the agent signals success; on success
  `integration_status = done` and the normal agent-success transition moves the
  parent on (`work → testing`). A failed verify keeps it `pending`, so the rework
  run gets the integration brief again. Adding a new subtask resets the status.
  **Fallback:** if the parent sits on an `agent_ignore` label, or no agent is
  configured for its label (so nothing would ever run), the old behaviour is kept:
  with no conflicts the parent auto-advances along its agent-success transition,
  recorded with the `subtasks_complete` trigger.

Manage subtasks from a task's detail page (the Subtasks section) or via
`POST /tasks/{id}/subtasks` and `GET /tasks?parent_id=`. Task responses carry
`parent_task_id`, `merge_status`, and derived `subtask_total`/`subtask_done`/
`subtask_conflicts`, plus `file_scope` and (for parents) `integration_status`.

### Planner prompt guidance

Fan out only when it pays off: **3 or more independent parts that touch disjoint
files.** Two parts, or parts sharing files, should stay one task (overlapping
scopes serialize, which loses the benefit). This guidance is not applied to
existing agent configs; copy it into a planner's system prompt if you want it:

```text
Decide whether to fan out. If the work splits into 3 or more independent parts
that touch disjoint files, call create_subtask once per part with a clear
description and a directory-rooted file_scope (for example
"backend/internal/foo/**"). Give every subtask a file_scope. Subtasks whose
scopes overlap run one after another. Otherwise do not fan out: write a single
plan for the whole task.
```
