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
| `display_name` | string, optional | Human-readable title shown in the UI (column headers, chips, graph nodes), at most 64 characters, whitespace-trimmed. Empty or omitted means the UI derives one from `name` (`-`/`_` become spaces, sentence case: `not_ready` → "Not ready"). UI-only: `name` stays the identifier in the API, URLs and transitions. The Default workflow seeds friendly names. |
| `wip_limit` | int, nullable | Max number of (non-archived) tasks allowed on this label. `null`/unset = unlimited — a label with no limit behaves exactly as before this feature existed. Must be a positive integer when set; `0` or negative is rejected. |
| `wip_limit_hard` | bool | Enforcement mode for `wip_limit`. **Soft (default, `false`)**: the board column header shows `count / limit` and flags the column once its count reaches or exceeds the limit, but nothing is blocked. **Hard (`true`)**: in addition to the visual flag, the dispatcher will not pick up a task whose agent "success" transition would land it on this label while the label's count is already at or over its limit — the task simply stays where it is and is re-evaluated on the next sweep (a few seconds later), so work queues upstream instead of erroring mid-run. Hard mode never blocks a run that is already in flight from landing in a full column; that stays visual-only. Setting `wip_limit_hard` without a `wip_limit` has no effect (there's nothing to enforce). |
| `verify_mode` | `inherit` \| `custom` \| `disabled` | Per-label override of the repo's [verify command](#verify-commands). `inherit` (default, may be omitted) uses the repo command; `custom` runs this label's `verify_command` instead; `disabled` skips verification for runs that succeed on this label. |
| `verify_command` | string | The label's own command. Required when `verify_mode` is `custom`; ignored (and cleared) otherwise. The repo's `verify_timeout_seconds` applies. |

## Pipeline overview

The **Pipeline** page (`/pipeline`, Overview tab) draws the selected workflow as a graph, left to right in board column order (vertically on phones). Labels are nodes, transitions are edges (green success, red failure, dashed for agent-triggered); failure/rework edges loop above (human-triggered) or below (agent-triggered) the main line. Each node shows the label name and who works it: the first enabled agent config covering the label with its provider / model (`+N` for more), **Human gate** for labels only a human can move, or an amber **No agent** chip with an **Assign** button for agent-triggerable labels nobody covers. Badges show **Opens PR**, **WIP n**, a non-default **Verify** mode and **Terminal**. Text never renders below about 12px: when the pipeline is wider than the page it keeps that size and you pan (drag or scroll) instead, with a hint shown. The canvas follows the light/dark theme. The Board shows the same chip in the header of unstaffed columns.

Click a node to open the assign panel: tick or untick an agent config to add or remove that label from its `labels` (the same update the Agents tab performs). Use **Edit workflow** (see below) or the Workflow tab to change the labels and transitions themselves. With several workflows, a selector above the graph switches between them.

**Autonomy dial.** Autonomy gates (see [Autonomy per gate](#autonomy-per-gate)) show a dial chip (**Always ask**, **Ask on flags**, **Auto**) instead of "Human gate"; labels that are only `agent_ignore` keep "Human gate". Click a gate to open the gate panel under the graph:

- **Gate mode** segmented control and, for Ask on flags, the seven flag checkboxes ("Ask when the brief flags", disabled unless the mode is Ask on flags).
- **Guardrails**: fixed chips "Verify must pass" and "Breaker stops", a **Hold** select (No hold / 10 min / 1 h) and an optional **Max cost (USD)**.
- **Warnings** when Auto sits on a gate whose success target opens a PR ("PRs will open without anyone reading the diff"), and when repos using this workflow have no verify command.
- **Backtest** for the last 30 days, recomputed (debounced) as you change settings: would skip / would ask / rejections missed, a strip with one cell per pass (asks, auto-approved, rejected-caught, rejected-slipped) with a legend, and a one-line verdict.
- **Ask rules** for the gate (including all-gates rules) with who added them, the source task, hit count, an **Unused** chip when there was no hit in the last 60 days, and Delete.
- **Suggested ask rules** (model-proposed, see [Suggested ask rules](#suggested-ask-rules)): pick the provider config that writes them, **Find rules**, the last run's result, and one card per suggestion with what it would have caught and cost, the model's reason, links to the caught tasks, **Add as ask rule** and **Dismiss**. Ideas for new checks from the last run are listed underneath.
- **Escapes and history**: recent mode changes including automatic step-downs ("2 escapes in 14 days. Moved from Auto to Ask on flags") with a **Change back** button.

Save rewrites the workflow through the YAML update (`PUT /workflows/{id}/yaml`) after re-reading the workflow, so everything else (including `create_pr` and per-transition agents) is preserved. "No diff yet" is a heuristic: when every agent stage upstream of the gate is named like a plan stage (or there is none), tasks have no code changes there, so only Always ask / Auto are offered and a note says Ask on flags would always ask. A saved Ask on flags value is never changed silently.

On phones the page lists the gates under the graph (dial chip per gate); tapping one opens a bottom sheet with the segmented control, a one-line backtest summary, "Flags that ask: n of 7 · Edit" and Save. `/pipeline/overview?gate=<label>` opens a gate directly.

## Editing the workflow graph

The Pipeline Overview graph has an **Edit workflow** button. Edits are staged locally (an "Unsaved changes" indicator shows, with **Save** and **Discard** buttons) and nothing is written until you save. The YAML editor on the Workflow tab stays available as the Advanced view; both write the same model.

Supported edits:

- **Add a label** (name and color) from the side panel; **rename** or **delete** the selected label. Renaming rewrites its transitions; deleting also removes the transitions touching it. Labels that currently have tasks on them cannot be renamed or deleted (tasks reference labels by name).
- **Connect two labels**: drag from a stage's right dot to another stage's left dot (bottom to top on phones) (creates an agent-on-success transition), or use the **Connect labels** form (from, to, type) for keyboard use. Each transition in the **Transitions** list has a type selector (agent on success, agent on failure, agent either outcome, human, agent or human) and a delete button.
- **Per-label settings** (select a stage): whether agents work it (`agent_ignore`), **terminal**, **open a PR on entry** (`create_pr`, at most one label), **WIP limit** (and hard limit), and **verify mode** (inherit / custom with a command / disabled).
- **Human approval gate**: the checkbox shows the gate's autonomy mode read-only with a link to the Pipeline gate panel; unticking it on a label with autonomy resets autonomy to Always ask. The checkbox makes every non-failure transition out of the label human-triggered (and turns them back into agent-on-success when unticked).

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

When a task sits on a **human-gate** label (no agent-triggerable way out and at least one human transition, the same rule as `GET /needs-me` `human_gate`; `agent_ignore` backlog labels excluded), the Task Detail page shows a compact **Review brief** below the task header on the Overview tab and above the diff on the Diff tab, and the Needs-you review popup and Home's Needs-you panel show its one-line summary, diff size, cost, verify result, flag chips and preview count. It is computed from existing data with **no LLM calls**, and it is **advisory only**: nothing is ever approved or blocked because of a flag.

On screens at least 1280px wide, a task on a human gate (strict rule: every exit is a human transition, or `agent_ignore`) gets a decision-first layout: a header with title, state, repo, cost, run count and the Approve / Request changes buttons ("Approve and open PR" when the target label opens a PR); the diff (with preview strip) or, on plan-review labels, the plan in the main pane with logs and details as tabs; and a sticky right rail with the brief, verification and runs. Tablets (768–1279px) stack the brief, verification and runs above the diff, with the buttons in the header. Phones (under 768px) show a compact brief card (changed / risks / verified, with a "Full brief" toggle), the diff or plan below it, and Approve / Request changes pinned in a bottom action bar above the tab bar. Escalated `waiting_human` runs and non-strict gates keep the tabbed layout with the reply box. Dependencies, Force move, label history and source comments sit under a collapsed **Details** toggle (remembered per browser).

The brief is served by `GET /tasks/{id}/review-brief`; the review popup gets the same context from `/needs-me` (`risk_flags`, `summary`, `changed`, `cost_usd`, `verification`, `preview_count`; human-gate items only, best effort).

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

**Storage.** PNGs are stored in `UPLOAD_DIR/<task_id>/preview-<uuid>.png` and listed in `task_preview_images`; they are *not* task attachments, so they are never fed to agents. The latest 3 captures per task are kept; deleting the task removes the files. Screenshots appear in the review brief card (Task Detail and the Needs-you review popup) as a thumbnail strip that opens full size.

**Failures never block.** Any problem (command exits, port never opens, browser missing, one route failing) is recorded on the capture and shown as "Preview failed: ..." in the brief; the task label, failure counts and run status are untouched. A capture left `running` by a server restart is marked failed at startup.

**v1 limits.** After-only: there is no base-branch "before" shot yet (the `variant` column leaves room for it). Dev servers that daemonize into a new session escape the process-group cleanup. See [runtime.md](runtime.md#preview-screenshots) for the browser setup.

## Autonomy per gate

A **gate** is a label that waits for a person: every transition out of it is human-triggered, it has at least one, and it is not `agent_ignore` or terminal. This is the same rule as the Inbox `human_gate` item. A gate can carry an optional `autonomy` block saying when a person need not be asked:

The Pipeline overview has a panel for editing this per gate; see [Pipeline overview](#pipeline-overview).

```yaml
- name: review
  autonomy:
    mode: on_flags        # ask | on_flags | auto
    ask_on: [migration, auth_or_security, dependencies, tests_removed, ci_config]
    hold: 10m
    max_cost_usd: 5
```

| Mode | Meaning |
|---|---|
| `ask` | Default (the same as no block). Always wait for a person. |
| `on_flags` | Approve when the [review brief](#review-brief) raises none of the `ask_on` flag codes. An empty `ask_on` blocks on no flag, only on the hard stops. |
| `auto` | Approve whenever the hard stops below pass. Flags are ignored. |

`ask_on` takes the review-brief flag codes (`migration`, `auth_or_security`, `dependencies`, `tests_removed`, `ci_config`, `large_change`, `outside_plan_scope`). `hold` is a duration (`90s`, `10m`, `1h`; default 10 minutes). While an approve decision waits out this window the task shows as auto-approving and can be held. `max_cost_usd` is optional.

**Hard stops** (always ask, whatever the mode):
- the latest run's verification is `fail`, `timeout` or `error` (no verify configured is not a stop);
- the task has the per-task `always_ask` override (`PATCH /tasks/{id}/always-ask` with `{"always_ask": true}`);
- `max_cost_usd` is set and the task's total cost is strictly above it;
- for `on_flags`, the task has no diff against its base (for example a plan-review gate), because flags cannot be computed.

**JSON.** The workflow REST API returns and accepts `autonomy` on each label: `null` for ask, otherwise `{mode, ask_on, hold_seconds, max_cost_usd}`. The YAML export only emits the block for non-ask labels, so the block round-trips unchanged through `get_workflow` / `update_workflow`.

**Validation (400).** The mode must be one of the three (and is required inside a block); `ask_on` codes must be known flag codes; `hold` and `max_cost_usd` must not be negative; and a non-ask mode is only allowed on a gate (the error says so). Un-gating a label that has autonomy needs the block removed in the same save.

**Acting on decisions.** When a task lands on a gate whose mode is not `ask`, the server evaluates the policy (deterministic review brief only, never an LLM) and records the decision (`approve` or `ask`, with reasons) in `autonomy_decisions`. Read the latest for the task's current label with `GET /tasks/{id}/autonomy-decision` (404 when there is none). Recording is asynchronous and best effort: a failure is logged and can never block a transition.

An `approve` decision takes effect after the label's `hold` window (default 10m; `0` approves immediately). Until then the decision row is `pending` with a `due_at`, and the task shows as `auto_approving` in `GET /needs-me` (never also as `human_gate`). A single background loop (`internal/autonomy/policy`, sweeping every 10s and once at startup, so pending approvals survive a restart) approves due rows only if the task is still on that gate label, is not paused, has no live run and no `waiting_human` run, and the row was not held. Otherwise the row is `superseded` (task moved) or `skipped` (paused / live run) and the task is a normal review.

The approval goes through the same code as `POST /tasks/{id}/approve` (`workflow.Engine.Approve`), so `create_pr` targets, side effects, WebSocket events and label history behave identically. The trigger is `human` and the recorded actor is `policy` (that name is reserved: `API_TOKENS` entries called `policy` are ignored). The label-history note reads like `Auto-approved: gate set to Ask on flags; no flags; verify passed; held 10m`.

**Hold** (`POST /tasks/{id}/autonomy/hold`) cancels a pending auto-approval; the decision row becomes `held` and the task is a normal review (and sends the usual notification). **Undo** (`POST /tasks/{id}/autonomy/undo`) works only when the task's last gate exit was by `policy`, the task is not terminal-merged and has no live run. It moves the task back to that gate (label history actor = the caller, note `Undo auto-approval`) and records a row in `autonomy_escapes` (source `undo`). If a pull request was already opened it is left as is and the response says so. When the task lands on the gate again the `undone` hard stop forces `ask`, so it is not re-approved automatically.

**Notifications.** The outbound webhook no longer fires for a `label_changed` into a non-`ask` gate. It fires (as `human_gate`) when the policy hands the task to a person (`task.autonomy_asked`) or a pending approval is held (`task.autonomy_held`). Known limitation: the frontend's browser notifications still fire on a human-gate `label_changed`.

**Backtest.** Before changing a gate, `POST /workflows/{id}/labels/{label}/autonomy/backtest` with `{mode, ask_on, max_cost_usd, days}` (`days` defaults to 30, max 90; over 90 is a 400) shows what that setting would have done to recent passes. It is read-only. Each human exit from the gate in the window (forced moves ignored) is one pass: `approved` if it left via the success transition, `rejected` if via the failure path. Inputs are recomputed as of the pass: the branch diff at the newest branch commit dated before it, the verify status and total cost of the runs up to then, and the task's current `always_ask`. Flags are only computed for `on_flags`; if the branch or commit is gone the pass is `unknown` and left out of the counts. The response has `total`, `evaluated`, `unknown`, `would_skip`, `would_ask`, `rejected`, `escaped` (rejected *and* would be skipped) and `passes` (oldest first). It looks at the 500 most recent passes (`truncated: true` if more) and after about 25 seconds the unprocessed passes come back `unknown` with `timed_out: true`.

**Backtesting a draft rule.** The backtest body also accepts `extra_rules` (a list of rule conditions, each optionally with a `repo_id`) and then returns a `rules` block (`null` otherwise): `{policy_approvals, evaluated, unknown, would_stop, stopped: [{task_id, title, at, rule_indexes}]}`. It replays the draft rules against the (up to 500) recent policy approvals at the gate, using each task's diff as of the approval, and counts how many it would have stopped. Stored rules also apply to the normal backtest counts. Neither touches hit counters.

### Ask rules

An **ask rule** forces a human decision at a gate for tasks that look like ones that have slipped through before. A rule matches when **all** of its set conditions match:

| Field | Condition |
|---|---|
| `path_globs` | any glob matches any changed file (same syntax as [lesson](lessons.md) globs: `**`, `*`, `?`, a pattern without `/` matches the basename, a trailing `/` is a directory prefix) |
| `task_type` | equals the task's type |
| `min_changed_lines` | additions + deletions of the task's diff is at least this |

A rule needs at least one condition. Scope: `workflow_id` (required), `label` (null = all gates of the workflow), `repo_id` (null = all repos). A match adds an `ask_rule` reason (with `rule_id`) and forces `ask` in modes `on_flags` and `auto`, and increments the rule's `hit_count` / `last_hit_at` on real evaluations (never in a backtest). Conditions that need a diff (`path_globs`, `min_changed_lines`) do not match when there is no diff (no branch, or a plan gate). Rules are managed with `GET/POST/PATCH/DELETE /autonomy/ask-rules`; the `label` and `repo_id` list filters also return the all-gates / all-repos rules because those apply too. Rules are never deleted automatically, and renaming a label leaves rules pointing at the old name (labels are name-keyed).

### Escapes

An **escape** is a task that got through a gate and should not have. Each is a row in `autonomy_escapes` tied to the policy approval (`decision_id`). Sources:

- `undo`: someone used Undo on an auto-approval.
- `should_have_asked`: someone reported it (below).
- `rejected_later`: a person rejected the task (its failure path) at a later human gate within 14 days of the approval.
- `reopened`: a person moved the task out of a terminal label within 14 days.

The last two are detected automatically when a human moves the task. The detection is best effort (the policy queue drops events when full) and records at most one escape per approval; an explicit `should_have_asked` or `undo` is always recorded.

### Step-down

When a gate reaches **N** distinct escaped approvals within **W** days it is lowered one notch (`auto` → `on_flags` → `ask`) by the server, which never steps up automatically. N and W are set at `GET/PUT /settings/autonomy` (`step_down_escapes`, default 2, `0` = never step down; `step_down_window_days`, default 14). One bad approval reported twice (for example undo and should-have-asked) counts once. The counter restarts after any change to the gate's autonomy, automatic or by a workflow update. Stepping `auto` → `on_flags` with an empty `ask_on` sets `ask_on` to every flag code (otherwise the step would change nothing). Pending auto-approvals at the gate are cancelled (`superseded`, with a `task.autonomy_asked` event, reason `stepped_down`), so a person decides. Every change is recorded in `autonomy_changes` (actor `policy` for a step-down, otherwise the caller of the workflow update) and published as the `autonomy.stepped_down` WebSocket event and webhook. `GET /workflows/{id}/labels/{label}/autonomy/history` returns the changes and escapes (newest first) with the current step-down counter.

### Suggested ask rules

A model can read a gate's history and suggest ask rules that would have caught the tasks that slipped through ([ADR 0040](adr/0040-llm-proposes-autonomy-rules-never-decides.md)). It only ever **proposes**: the gate decision itself (`autonomy.Evaluate`) never calls a model, and a suggestion changes nothing until a person adds it, which creates a normal ask rule. An ask rule can only add a reason to ask, so a suggestion can cause more asking, never less.

- **Off until configured.** `PUT /settings/autonomy` with `proposal_provider_config_id` (a [provider config](agents.md); null turns it off) and `proposal_interval_hours` (default 24, `0` = only on demand). The gate panel has the same picker.
- **What it reads.** For one gate, the last 30 days: escapes (with their notes; facts as of the policy approval) and human rejections (failure-path exits, with the note), one entry per task, up to 40; plus the gate's clean policy approvals (not escaped), up to 500. With fewer than 3 escapes and rejections the run is `skipped` without a model call.
- **How it asks.** One run of the provider's CLI outside any task, in an empty scratch directory, with shell commands denied and no MCP sidecar. Task titles, notes and file paths are fenced as untrusted data. The answer must be a JSON block of at most 5 rules and 3 flag ideas.
- **What survives.** Each rule must pass the normal ask-rule validation (at least one condition, valid globs, a known repo); it is scoped to the gate (optionally one repo), and anything else is dropped, not repaired. A rule identical to an existing rule at the gate is dropped. The server then replays each rule with the same matcher the evaluator uses: **caught** = escapes and rejections it would have stopped, **cost** = clean approvals it would also have stopped, **unknown** = tasks whose diff is gone for a path or size condition. A rule that catches nothing is dropped.
- **Accept / dismiss.** `POST /autonomy/rule-proposals/{id}/accept` creates the ask rule (label = the gate, `created_by` = you) and records its id on the proposal. Dismissed suggestions are remembered by their conditions: a later run that finds the same rule refreshes its counts but does not offer it again.
- **Schedule.** Every hour the server runs gates whose last run is older than the interval, one at a time; scheduled runs skip gates in `ask` mode (a rule changes nothing there), while **Find rules** (`POST /workflows/{id}/labels/{label}/autonomy/proposals/run`) works on any human gate. Runs are recorded in `autonomy_proposal_runs` (status, error, cost and tokens, flag ideas; kept 90 days) and listed by `GET /workflows/{id}/labels/{label}/autonomy/proposal-runs`. Their cost is not part of any task's cost.
- **Flag ideas.** A run may suggest a new review-brief check (for example "lockfile changes outside `*.lock`"). It is shown for a person to read; flag codes stay a fixed set and nothing can add one at runtime.

### Should have asked

`POST /tasks/{id}/autonomy/should-have-asked` is for a task the policy approved. Body: `{rule?, scope_repo, label_scope: "this_gate"|"all_gates", send_back_note?, lesson?: {body, kind?}}`. It records an escape (source `should_have_asked`), creates the ask rule when `rule` is given (for this gate or every gate of the workflow; for this repo when `scope_repo`), sends the task back through its failure path with the note (the normal reject path; skipped for a done task, and a failure is reported in `send_back_error` without undoing the rest), and creates an active lesson using the rule's globs. Everything is validated before anything is written. It is a 409 if the policy did not approve the task (create a rule with `POST /autonomy/ask-rules` instead).

`GET /tasks/{id}/autonomy/suggestions` proposes rule conditions from the task: the deepest common directories of the changed files as `dir/**` (top 3 by file count), `*.ext` for specific kinds (`.sql`, `.proto`, `.yaml`, ...), the task type, and a size threshold rounded down from the changed lines (to one significant digit).

The UI for this is a **Should have asked** button on "Approved for you" rows and their review popup, on the policy entry in Task Detail History, and in the Task Detail header for any task whose last gate exit was by `policy`. It opens a dialog (a bottom sheet on phones): tick the suggested conditions that should have stopped it (several ticks combine into one rule that needs all of them; ticked path globs match if any changed) or "Nothing specific, just log it"; choose this repo / all repos and this gate / all gates; watch a debounced line from the backtest ("This rule would also have stopped N of the M tasks auto-approved in the last 30 days", counted at this gate or at every gate with autonomy on); send the task back to work with a note (hidden when it is done); and optionally save a lesson for the ticked paths (prefilled, 300 characters). The toast names what was created.

Task Detail shows the decision as a banner under the review brief (inside the compact brief card on phones): green "This would auto-approve" or neutral "The gate would still ask you" with flag chips and hard stops; nothing shows in `ask` mode. Under **Details**, the "Always ask for this task" switch sets `always_ask` (with Undo).
Home lists pending approvals under **Auto-approving** (countdown, Hold; the Undo toast after Hold approves the task immediately, since a hold can't be re-armed) and recent ones under **Approved for you today** (Undo with confirm). Neither counts toward the needs-you badge. Task Detail shows the decision as a banner under the review brief (inside the compact brief card on phones): green "This would auto-approve" or neutral "The gate would still ask you" with flag chips and hard stops; the review header shows the countdown and Hold while pending, and History shows "Approved by policy" with the reason. Under **Details**, the "Always ask for this task" switch sets `always_ask` (with Undo).

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
    display_name: Human review # optional UI title
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
