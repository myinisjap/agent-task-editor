# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

To cut a release, add a `## [x.y.z] - YYYY-MM-DD` section below with the changes,
then push the matching tag (`git tag vx.y.z && git push origin vx.y.z`). The
release workflow builds and pushes the images and creates a GitHub Release using
this file's section for that version as the release notes.

## [Unreleased]

### Added
- **Agent-driven subtask decomposition** (#82, Mechanism 2). A planning agent can now
  split a large task into structured, dispatchable child tasks instead of leaving prose in
  `agent_notes`. Children are an implementation mechanism; the parent's branch (and single
  eventual PR) stays the only outward-facing artifact. Built on the Mechanism 1 dependency
  gate. Details:
  - **`create_subtask` MCP tool** (claude/qwen_code), exposed only when the run's agent
    config opts in (`subtasks_enabled`, off by default; `max_subtasks` caps children,
    default 10). Unlike the deferred result-file tools it writes **live** through the backend
    (`POST /tasks/{id}/subtasks`), so children appear on the board mid-run and the agent gets
    real task ids back. Guardrails: opt-in per config, per-parent cap, depth limit 1 (a
    subtask can't create subtasks), and a human gate — children land on the workflow's first
    `agent_ignore` label and a human releases them.
  - **Relationship model:** `tasks.parent_task_id` (grouping/rollup/provenance,
    `created_by_run_id`) plus an auto-created parent→child dependency edge (the dispatch
    gate). Deleting a parent orphans children to top-level rather than deleting them.
  - **Branch off parent, merge back:** a child's worktree is cut from the parent's branch. On
    reaching a terminal label a child's branch is **merged back into the parent's branch** (a
    plain merge commit, keeping per-child history) and its worktree/branch are removed —
    children never push to origin or open PRs. A conflicting merge-back is aborted cleanly,
    the child is flagged `merge_conflict`, and the parent's `work` agent is handed the
    conflict context to resolve it (`tasks.merge_status`: ``/`pending`/`merged`/
    `merge_conflict`).
  - **Parent auto-advance:** once every child is terminal and merged cleanly, the parent
    advances along its agent-success transition (`work → testing` in the seed workflow),
    recorded in history with a distinct `subtasks_complete` trigger. Degrades gracefully
    (leaves the parent for a human / the next dispatch) when it's paused, has a run in flight,
    or has no agent-success transition.
  - **UI:** child cards carry a `↳ subtask` badge (click → parent) and a merge-status badge;
    parent cards show a `⑃ done/total` rollup with a conflict indicator; the task detail page
    gains a Subtasks section (parent link + merge state for a child; children list + bulk
    "release" for a parent); `GET /tasks?parent_id=` filters to one family. Agent config gains
    a "Subtasks" toggle, and the seeded Planner template enables it.

- **Task dependencies — "don't dispatch B until A is done"** (#82, Mechanism 1). Tasks can
  now declare peer dependencies: a task with any unsatisfied blocker is never picked up by
  the dispatcher, though a human can still move it anywhere on the board. A blocker is
  satisfied once it reaches a terminal label or is archived, so there are no invisible
  deadlocks. Blocked-ness is *derived at read time* — there's no status column to drift and
  no event needed when a blocker finishes; the next dispatch sweep simply sees the task
  differently. Details:
  - New `task_dependencies` table (migration `028`); both foreign keys cascade on delete, so
    deleting a task unblocks its dependents. Edges are within a single workflow in v1.
  - `ListAgentPickupTasks` grows a `NOT EXISTS (unsatisfied blocker)` clause — the whole gate
    is one SQL predicate alongside the existing pause/archive/retry filters.
  - Endpoints: `GET/POST /tasks/{id}/dependencies` and `DELETE /tasks/{id}/dependencies/{dep_id}`.
    Adding an edge rejects self-edges and cross-workflow edges (`400`), blockers whose workflow
    has no terminal label (`400`, an edge there could never satisfy), and cycles or duplicates
    (`409`, the error names the cycle path). Cycle validation runs transactionally.
  - Task list/detail responses carry derived `blocked_by_count` / `blocking_count` so the board
    renders "blocked by N" badges in one query (no N+1). Blocked cards are visually muted, and
    dragging a blocked card into an agent-triggerable column pops a confirmation.
  - Task detail gains a Dependencies section: a same-workflow blocker picker with live met/unmet
    state, plus the list of dependents. Edge changes and blocker completion refresh badges live
    via `task.updated` events.

  _Not included in this change:_ Mechanism 2 of #82 (agent-driven subtask decomposition — the
  `create_subtask` MCP tool, child branch/merge-back, conflict handling, and parent auto-advance)
  is a separate follow-up that builds on this dependency primitive.

### Fixed
- **Concurrent workflow transitions no longer race** (#49). `workflow.Engine.Transition`
  now performs the label update as a compare-and-swap (`… WHERE id = ? AND label = ?`,
  the expected from-label): if a concurrent transition already moved the task, the
  update matches 0 rows and the engine returns the new `ErrStale` sentinel instead of
  silently clobbering the other write and recording two history rows from the same
  source label. Handlers map `ErrStale` to HTTP `409 Conflict` so the UI can refresh
  and retry.
- **Orphaned `pending` agent runs no longer linger** (#50). The dispatcher now creates
  the run row and sets the task's active-run pointer in a single transaction (either
  both land or neither does), and the startup crash-recovery sweep marks runs stuck in
  `pending` — not just `running` — as `failed`, so a crash between the two writes can no
  longer leave a task permanently locked behind a run nothing points at.
- **ghsync no longer polls dead tasks forever** (#52). The GitHub PR-status sweep now
  selects only branch-bearing, non-archived tasks that aren't already in a terminal PR
  state (`pr_merged` **or** `pr_closed`) via a new `ListGhSyncEligibleTasks` SQL query,
  instead of listing every task and filtering in Go. Tasks that never get a PR, or whose
  PR was closed without merging, no longer cost a `gh` call on every sweep — keeping the
  external-call rate bounded by open work as the task table grows.
- **Repo auto-clone no longer times out or leaves partial directories** (#53). `git clone`
  for a `remote_url`-only repo now runs asynchronously (the repo row is created with
  `clone_status: cloning` and `POST /repos` returns immediately), so a slow clone of a
  large repo can't exceed the server's 60s `WriteTimeout` and get cut off mid-clone. On
  completion the row flips to `ready` (or `error` with `clone_error`, removing the partial
  clone directory) and a `repo.clone_done` / `repo.clone_failed` WebSocket event fires.
  The `Create` base-dir containment check now also resolves symlinks (via
  `filepath.EvalSymlinks`), matching `Update`, so a symlink under the base dir can no
  longer pass validation on create but fail on update.
- **`~/.claude.json` trust updates are now atomic** (#55). `setClaudeTrust` writes to a
  temp file in the same directory and `os.Rename`s it over the original (preserving mode
  `0600`) instead of rewriting in place, so a crash or a concurrent claude-CLI subprocess
  write can no longer corrupt the file and break every subsequent claude-provider run.

### Changed
- Centralized the previously-scattered provider error classification (login
  detection, transient-infra detection, and rate-limit detection) into a single
  pattern table in `backend/internal/agent/errclass.go` with per-pattern unit
  tests, so adapting to a CLI wording change is a one-line edit instead of a hunt
  across three files. Every failure now resolves to one explicit
  `classification` (`genuine`/`transient`/`rate_limit`/`auth`) that is logged on
  the failure log line, making misclassifications diagnosable from logs. The
  claude/qwen providers now prefer the typed stream-json `result` event over raw
  stdout/stderr sniffing when classifying a run.

### Security
- Fixed an authentication bypass where any request carrying an
  `Upgrade: websocket` header skipped bearer-token validation on **every** API
  route (not just `/ws`), letting a client read and write the full API without a
  token when `API_TOKEN` was set. The `/ws` route is now mounted outside the
  `BearerAuth` middleware (it does its own constant-time `?token=` check), and
  the header-based bypass has been removed.

### Added
- **Session continuity across runs** (#77). The `claude` provider now records each
  run's conversation `session_id` (from the CLI's stream-json envelope) and, when
  the same agent config runs the task again — a review rejection back to `work`, a
  re-run after a failure — resumes it with `claude --resume` instead of starting
  cold, so the agent keeps its full prior context instead of re-reading the repo
  to address a one-line note. Resumed runs send a condensed prompt (only the new
  feedback/reply/review comments); if the session no longer exists the runner
  falls back to a cold start automatically. Per-agent-config `resume_sessions`
  toggle (default on) for stages that should look at the work with fresh eyes.
  `qwen_code` records its session id but is not resumed yet.
- **Reply to a waiting agent** (#78). When a run pauses on `waiting_human`
  (`request_human`), a reply box on the task detail page — backed by
  `POST /api/v1/tasks/{id}/runs/{run_id}/reply` — answers the agent's question
  with text and starts a continuation run that resumes the same session (or
  starts cold with the reply injected as `RESPONSE FROM HUMAN` for non-resume
  providers). The task stays on its label — a reply is a conversation, not a
  workflow transition — the replied-to run keeps `waiting_human` (approve/reject
  parity), and the reply is recorded at the top of the new run's log.
- Cancel a running agent run: `POST /api/v1/tasks/{id}/runs/{run_id}/cancel` plus
  a **Stop run** button on the task detail page. The pool keeps a per-run cancel
  registry; cancelling cancels the run's context (killing CLI subprocesses via
  `exec.CommandContext` and aborting HTTP providers), then marks the run
  `cancelled` (not `failed`, and without consuming transient-retry budget),
  pauses the task so it isn't immediately re-dispatched, clears the active-run
  lock, and broadcasts `task.agent_done`. Fills the kill-switch gap where
  pausing only blocked *future* dispatch — a runaway agent no longer burns
  tokens until it times out.
- Provider health / onboarding status page (`Health` in the sidebar) backed by a
  new `GET /api/v1/health/providers` endpoint. Checks the claude CLI (present +
  authenticated), API keys for the anthropic/llm providers, qwen/opencode
  binaries (only for providers an enabled agent config uses), the MCP sidecar
  binary (`MCP_SERVER_PATH`), gh auth, and `REPO_BASE_DIR` — each rendered as a
  green/yellow/red row with a one-line fix hint. Turns the most common "why did
  my first run fail" support loop into a glance.

## [0.1.0] - 2026-07-04

First tagged release.

### Added
- Versioned multi-arch (amd64 + arm64) Docker images published to GHCR on every
  `v*` tag push, plus an automated GitHub Release.
- `docker-compose.release.yml` for running from the prebuilt
  `ghcr.io/myinisjap/agent-task-editor-{backend,frontend}` images instead of
  building from source.
- `run.sh` helper that injects the runtime env vars (repo mount, GitHub token,
  Claude auth, SSL bypass) and starts the stack from published images.
- Runtime `PUID`/`PGID` remap in the backend container: it steps down to the
  host user at startup (via an entrypoint + `su-exec`) so files agents write to
  bind-mounted repos are owned by you. Works for prebuilt images with no rebuild.
- Frontend unit tests (vitest) for the `src/lib` parsers — `parseAgentLog`,
  `parseWorkflowYaml`/`validateWorkflow`, `parseDiff`, `condensedBoard`, and
  `diffComments` — with real captured fixtures, wired into the frontend CI job.
- This changelog.

### Changed
- Backend image runs as the host user at runtime via `PUID`/`PGID` instead of a
  build-time `HOST_UID`/`HOST_GID` remap. `dev.sh`/`run.sh` set these from
  `id -u`/`id -g`; the build no longer bakes in a UID.
