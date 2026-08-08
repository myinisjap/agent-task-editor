# Task Sources — Issue Import (GitHub / Gitea)

Tasks are normally created by hand on the board, but they can also be
**imported from an external tracker**. Two sources ship today: **GitHub
Issues** and **Gitea Issues** (against a self-hosted Gitea instance). A
background poller (`internal/tasksource`) sweeps every repo that has issue
sync enabled, creates a board task for each matching open issue, and keeps
existing tasks in step with their issue afterwards — see
[Keeping imported tasks in sync](#keeping-imported-tasks-in-sync).

Internally, issue import, PR-state sync (`internal/ghsync`), and PR
creation/write-back all go through a `Forge` interface
(`internal/forge`) rather than talking to a specific provider directly.
GitHub (via the `gh` CLI, `internal/ghclient`) and Gitea (via its REST API,
`internal/forge/gitea`) are the two implementations shipped today — every
prerequisite below that's phrased in terms of GitHub applies equally to
Gitea unless noted otherwise. The seam exists so an additional self-hosted
forge (e.g. GitLab) can be added as another `forge.Forge` implementation
without changing `tasksource`/`ghsync`/write-back themselves. Tasks imported
from a given forge carry that forge's name in `tasks.source` (`"github"` or
`"gitea"`); `Importer.resolveSource` picks the right `Source` for each repo
per sweep based on which forge recognises its remote URL, so a single
importer handles repos on either forge side by side.

Gitea support is configured via environment variables rather than per-repo
UI fields, since which Gitea instance(s) exist is host-level configuration,
not something that varies per repo the way `issue_sync_label` does:

| Env var | Meaning |
|---|---|
| `GITEA_HOST` | Required to enable Gitea support at all. Comma-separated host(s) this instance should recognise (e.g. `git.example.com` or `git.example.com,gitea.internal:3000`). With this unset, `GiteaIssues`/the Gitea `forge.Forge` are inert — no remotes match, and this is a safe no-op for anyone not running Gitea. |
| `GITEA_TOKEN` | A personal access token with repo read/write scope. |
| `GITEA_BASE_URL` | Optional override for the API base URL (defaults to `https://<first GITEA_HOST>`) — set this if the instance's API is reached at a different scheme/host/port than what appears in git remote URLs. |

See `internal/forge/gitea`'s package doc for the full detail on these.

These are passed through to the backend container by both `docker-compose.yml`
and `docker-compose.release.yml`, so set them in your shell or in the
repo-root `.env` file before running `./run.sh` / `./dev.sh`.

### Running the Gitea smoke test against a real instance

`internal/forge/gitea` is covered by ordinary httptest-server-backed unit
tests, which run as part of `go test ./...` with no external dependencies.
There is also an **opt-in** smoke test (`TestSmokeLiveInstance` in
`smoke_test.go`) that exercises the read-only surface (`AuthStatus`,
`ParseRepoName`, `ListOpenIssues`, `GetIssueComments`, `PRForBranch`,
`PRHead`) against a real, self-hosted Gitea instance. It never runs by
default — it's skipped unless explicitly opted into, so CI and local
`go test ./...` runs never require (or accidentally touch) a live Gitea
instance:

```bash
GITEA_SMOKE=1 \
GITEA_HOST=git.example.com \
GITEA_TOKEN=<token> \
GITEA_SMOKE_REPO=owner/repo \
go test ./internal/forge/gitea/... -run TestSmokeLiveInstance -v
```

Optionally set `GITEA_SMOKE_BRANCH` (defaults to `main`) to point
`PRForBranch`/`PRHead` at a specific branch. The test only performs
read-only calls — it never creates PRs, labels, or comments on the target
instance.

## Enabling it

Issue sync is configured **per repo** (Repos page in the UI, or the REST
API):

| Repo field | Meaning |
|---|---|
| `issue_sync_enabled` | `1` to turn the importer on for this repo |
| `issue_sync_label` | **Deprecated** (see [Intake routing rules](#intake-routing-rules) below) — only import open issues carrying this label (e.g. `agent-ok`). Empty = **all** open issues |

Two prerequisites are enforced when enabling:

1. **`remote_url`** must be set and point at a recognised forge — GitHub
   (issues fetched with the `gh` CLI; same auth as PR sync: `gh auth login`
   or `GITHUB_TOKEN`), or a self-hosted Gitea instance whose host is listed
   in `GITEA_HOST` (see above; issues fetched via Gitea's REST API using
   `GITEA_TOKEN`).
2. **A workflow** must be assigned to the repo — imported issues become tasks
   in that workflow.

Via the API:

```bash
curl -X PATCH http://localhost:8080/api/v1/repos/<id> \
  -H "Content-Type: application/json" \
  -d '{"issue_sync_enabled": true, "issue_sync_label": "agent-ok"}'
```

## What gets imported

For each matching open issue the importer creates a task with:

- **title** — the issue title
- **description** — the issue body, plus an `Imported from <issue URL>` line
- **type** — a heuristic over the issue's labels: `bug`/`defect`/`regression`
  → `bug`; `chore`/`maintenance`/`dependencies`/`ci`/`refactor`/`cleanup` →
  `chore`; `spike`/`research`/`question`/`investigation` → `spike`; anything
  else → `feature`
- **label** — the repo workflow's human-gate label (the lowest `sort_order`
  `agent_ignore` label, or the first label if none is marked `agent_ignore`),
  same as manually created tasks, so nothing runs until a human moves the task
  into an agent-triggerable column. In the default workflow this is `not_ready`.
- **source / source_ref** — `github` / `owner/repo#123`, shown as a link back
  to the issue on the task detail page

Pull requests are never imported. Issues are fetched from the REST API
(`gh api repos/{owner}/{repo}/issues --paginate`), which *does* return pull
requests, so the importer drops any entry carrying a `pull_request` field.
The fetch is fully paginated — earlier versions capped it at 200 issues per
sweep and silently ignored the rest, which also meant reconciliation (below)
could mistake a truncated page for a closed issue.

New tasks for one repo's sweep are created inside a **single database
transaction** (one commit for the whole batch, not one per issue) and, if
any were created, a **single `task.created_bulk`** WebSocket event is
published for the repo instead of one `task.created` per issue (see
[websocket.md](websocket.md)). Both changes exist to keep a large first
import (or backlog catch-up) from repeatedly acquiring SQLite's write lock
and from flooding connected clients with one event — and one board refetch —
per issue.

## Intake routing rules

Issue import, and separately [cron schedules](task-templates.md#recurring-schedules),
both create tasks. Before intake rules, the only shaping either path could do
was land the task on the workflow's gate label (issue import) or a single
fixed `target_label` (schedules) — there was no way to say "an issue labeled
`bug` from repo X should get the triage template, priority 1, and start on
`work` instead of the gate." Intake rules (`/intake-rules`, "Intake Rules" in
Configuration) close that gap: a small match→apply table evaluated at
task-creation time for the `issue` and `schedule` sources.

Rules are evaluated **first-match-wins**, in `sort_order` (then `created_at`)
order — the first *enabled* rule whose match conditions all hold applies, and
no rule after it is considered. This mirrors how the dispatcher walks agent
config `matchConfigs`.

**Match on:** source (`issue` or `schedule` — `manual`/`subtask` are valid
values in the schema for forward-compatibility but not currently evaluated by
any code path), repo, incoming labels (any-of, case-insensitive), a Go
regexp against the title and/or body, and the issue's author association
(`OWNER`/`MEMBER`/`COLLABORATOR`/`CONTRIBUTOR`/`NONE`, any-of). Every
match field left empty matches "any" for that dimension; all specified
fields must hold together (AND across fields, OR within a list).

**Apply:** a template (supplies `type`, and the template's own description is
prepended to the composed issue description — the issue's own **title**
always wins, since an imported task must stay identifiable against its
source), a priority, a target label, a workflow override, and/or a per-task
cost budget (`max_cost_usd`). Every apply field left unset means "leave the
caller's existing default" — a rule only has to say what it actually wants
to change.

`apply_template_id` only applies to `issue`-sourced matches. A scheduled
task is always shaped from the schedule's own template (the one a human
picked when creating the schedule, see
[task-templates.md](task-templates.md)) — there is no "apply a different
template on top of the schedule's own" semantics to fall back to. A rule
with `match_source: "schedule"` and `apply_template_id` set is rejected at
create/update time (400) rather than silently doing nothing; the scheduler
also logs a warning and ignores it defensively in the unlikely case a rule
reaches it in that shape anyway (e.g. a direct database edit).

The matched rule's id is recorded on the task (`matched_rule_id`, surfaced on
the task detail page as "Intake rule") so "why did this task land here with
this label/priority" is answerable from the task itself, rather than
requiring rule-table archaeology.

### The auto-start safety gate

This is the single most important behaviour to understand before using
target-label overrides. Imported issues normally land on the workflow's
human-gate label (the lowest-`sort_order` `agent_ignore` label) specifically
so a human reviews and promotes the task before an agent ever runs on it —
that review step is the mitigation for imported issue *bodies* being
untrusted, attacker-influenced input (an issue can contain anything; treat it
the same way you'd treat any other externally-supplied text reaching an
agent's context).

A rule that sets `apply_target_label` to anything other than that gate label
**removes that mitigation** — the created task can start running unattended,
on content nobody has looked at. This is useful (it's the main reason to want
target-label overrides at all), but it is only permitted when the rule *also*
restricts `match_author_assoc` to a non-empty list drawn exclusively from
`OWNER`, `MEMBER`, `COLLABORATOR` — i.e. only issues from people who already
have write access to the repo can skip the human gate. The API rejects
(400) any rule that would combine an agent-triggerable target label with a
missing or untrusted author-association constraint, the UI blocks submitting
such a rule client-side with an inline warning, and the importer itself
defensively re-checks and falls back to the gate label (logging a warning)
if a rule somehow reaches it in an unsafe shape (e.g. a direct database
edit). This is enforced in exactly one place in the code
(`intake.AutoStartAllowed`) so the rule can never be re-implemented
inconsistently.

Cron schedules do **not** go through this gate: a schedule's `target_label`
is already a human-configured, validated value (see
[task-templates.md](task-templates.md)), not third-party content, so the
concern this gate exists for doesn't apply there. Concretely, a rule with
`match_source: "schedule"` may set `apply_target_label` to any
agent-triggerable label without an author-association constraint — the API
does not require one for schedule-sourced rules, and the UI does not show
the auto-start warning or block saving in that case. (Requiring an author
association on a schedule rule would be nonsensical: a schedule firing has
no author to check.) A rule's `target_label` for a matched schedule only
takes effect when the schedule's own `target_label` is left empty — the
schedule's own setting always wins when
present.

### Preview

The rule editor's "Preview matches" button evaluates a rule (saved or still
being edited) against the repo's most recently imported tasks (up to 50),
using the exact same matcher (`intake.Match`) the importer and scheduler
call at runtime, so what's previewed is guaranteed to match what actually
happens. It previews against already-imported task history rather than
making a live forge API call, so a repo with no import history yet previews
as empty.

### Relationship to `issue_sync_label`

`issue_sync_label` is now **deprecated** but still honoured for one more
release; it is not being replaced by rules in a single migration because the
two mechanisms operate at different points in the pipeline:

- `issue_sync_label` narrows the **fetch** — it controls which issues the
  importer's API query asks the forge for in the first place.
- Intake rules only run **after** fetch, on issues that were already going
  to be imported — they control how a fetched issue is *shaped* (template,
  priority, label, workflow, cost), not whether it's imported at all.

Concretely: for a repo that still has `issue_sync_label` set, that setting
continues to decide *what gets imported*; any matching rule decides *how the
resulting task looks*. The two layer rather than compete. This is
deliberate — making rules the sole gatekeeper for *what* gets imported would
mean a repo previously scoped to (say) `bug`-labeled issues would suddenly
start importing every open issue the moment a `bug`-matching rule replaced
its `issue_sync_label`, which is very much not what an operator moving from
one to the other would expect. A migration converts every repo's existing
`issue_sync_label` into an equivalent enabled rule
(`match_source: issue`, `match_repo_id: <repo>`, `match_labels: ["<label>"]`)
automatically, so the two mechanisms don't have to be reconciled by hand —
but until `issue_sync_label` is removed in a future release, clearing it on
a repo that still wants the same set of issues imported means adding an
equivalent fetch-time mechanism yourself (there isn't one yet); don't clear
it expecting the generated rule alone to keep the same issues out.

## Deduplication

`(source, source_ref)` is unique across tasks (enforced by a partial unique
index), so an issue is only ever imported **once**. A second sweep doesn't
create a duplicate task — it updates the existing one instead, subject to the
policy in the next section. Closing the issue or finishing the task does not
cause a re-import.

**Deleting** an imported task while its issue is still open and still
matches the filter *will* re-import it on the next sweep. To keep an issue
off the board, remove the filter label from the issue (or close it) instead
of deleting the task.

## Polling interval

The importer sweeps on a fixed interval, configurable via the
`ISSUE_SYNC_INTERVAL` env var / `issue_sync_interval` YAML key (Go duration
syntax, default `60s`).

## Keeping imported tasks in sync

Import is not a one-shot copy. Each sweep does three things beyond creating
tasks for new issues:

1. **Field updates** — an issue whose title, body, or labels changed upstream
   has those changes written to its task.
2. **Reconciliation** — a task whose issue no longer appears in the fetch (it
   was closed, or lost the filter label) is flagged rather than left sitting on
   the board indefinitely.
3. **Comment ingestion** (opt-in) — the issue's comment thread is read onto the
   task and passed to the agent as untrusted context.

All three are governed per repo:

| Repo field | Default | Meaning |
|---|---|---|
| `issue_sync_update_policy` | `gate` | When upstream field/comment changes are applied. `gate` = only while the task is still on the workflow's human-gate label; `always` = at any label; `never` = never |
| `issue_sync_gone_action` | `flag` | What happens when the issue closes or stops matching the filter. `flag` = record it only; `archive` = also archive the task; `move` = also move it to `issue_sync_gone_label` |
| `issue_sync_gone_label` | _(empty)_ | Destination label when the action is `move`. Required when setting `move` |
| `issue_comment_sync_enabled` | `0` | `1` to ingest the issue's comment thread |

```bash
curl -X PATCH http://localhost:8080/api/v1/repos/<id> \
  -H "Content-Type: application/json" \
  -d '{"issue_sync_update_policy": "gate", "issue_sync_gone_action": "flag", "issue_comment_sync_enabled": true}'
```

### Field updates and the freeze policy

On a sweep, a task's `title`, `description`, and `type` are compared against
its issue and rewritten when they differ — so an edited issue body propagates,
and relabelling an issue `bug` → `chore` upstream updates the task's type
through the same label heuristic used at import. Nothing else is touched: the
task's label, archived flag, and write-back state are never changed by a field
update.

The default `gate` policy exists because the board and the issue can both be
edited, and there is no sensible automatic merge when they disagree. Under
`gate`, upstream wins only while the task is still parked on the human-gate
label — untouched by a human and not yet picked up by an agent — which covers
the common case of an issue being refined before work starts. Once the task
moves, it's frozen and the board is authoritative. Set `always` if you'd rather
upstream keep winning, or `never` to make import a pure one-shot copy.

An unset or unrecognized value reads as `gate`.

### Reconciliation of closed or unlabeled issues

Each sweep also asks the reverse question: which previously-imported tasks for
this repo did *not* appear in the fetch? Since only open, filter-matching
issues come back, a closed issue — or one whose filter label was removed —
simply drops out, which is how it goes unnoticed.

Those tasks get `source_state = "gone"`, surfaced on the task detail page as a
warning badge beside the issue link. The repo's `issue_sync_gone_action` then
decides whether anything further happens.

The default is deliberately to **flag and stop**. An agent may be mid-run, and
a closed issue is not always a cancelled one — someone may have closed it by
mistake, or as a duplicate that still needs the work. Flagging makes the
situation visible and lets a human decide.

Three guarantees hold regardless of the configured action:

- **A task with an active agent run is only ever flagged**, never archived or
  moved, even under `archive` or `move`. It's re-evaluated on the next sweep
  once the run finishes.
- **Reconciliation never deletes a task or clears its `source`/`source_ref`.**
  It only sets state and optionally archives or moves, so the re-import
  behavior described under [Deduplication](#deduplication) is unchanged.
- **The flag is reversible.** If the issue is reopened or regains the filter
  label, the next sweep clears `source_state` automatically.

One caveat worth knowing: reconciliation infers "gone" from absence, so if a
`gh` call ever succeeds while returning nothing (an auth scope change, for
instance), every imported task for that repo would be flagged in one sweep.
With the default `flag` action that's cosmetic and self-correcting on the next
successful sweep. It's a reason to prefer `flag` over `archive`/`move` unless
you have a specific need.

### Issue comment ingestion

The issue thread is where clarification usually happens *before* work starts,
and it was previously invisible: PR review comments are ingested (see the
section below), but nothing ever read the source issue. With
`issue_comment_sync_enabled`, each sweep reads the issue's comments into
`task_source_comments` (deduped by GitHub comment id), shows them on the task
detail page, and renders them into the agent's prompt under a
`SOURCE ISSUE COMMENTS` section.

Two filters apply, and both matter:

- **Write access only.** A comment is ingested only when its GitHub
  `author_association` is `OWNER`, `MEMBER`, or `COLLABORATOR`. Comments from
  outside contributors and drive-by accounts are skipped.
- **Our own write-backs are dropped.** Comments carrying the
  `<!-- agent-task-editor:writeback -->` marker this system posts are filtered
  out, so an agent never reads its own "a PR has been opened" notice back as
  human input.

Ingestion follows the same `issue_sync_update_policy` as field updates, so by
default comments stop flowing once the task leaves the gate label.

**This is a prompt-injection surface, and it is off by default for that
reason.** Without comment sync, injecting text into an agent prompt requires
filing or editing an issue that matches the sync filter. With it, anyone who
can comment on a synced issue can get text in front of an agent — a much lower
bar on a public repo, which is why ingestion is limited to authors with write
access. The rendered prompt section states explicitly that its contents are
data rather than instructions, wraps them in untrusted-content markers, and
strips the closing marker from every comment body so a comment can't forge the
delimiter and escape into trusted prompt context. Source comments are also
never passed to the MCP sidecar the way review comments are — there is no
resolve loop for them. See the notes on prompt trust boundaries in issue #79.

## Status write-back

Alongside issue import, a repo can opt in to writing task status back to the
GitHub issue an imported task originated from — so people watching the
tracker get a signal that an agent is already working on it, without having
to check the board.

| Repo field | Meaning |
|---|---|
| `issue_writeback_enabled` | `1` to turn write-back on for this repo's imported tasks |
| `issue_writeback_label` | Label applied when a task first leaves the human-gate label. Empty (default) = `agent-in-progress`. Must already exist on the GitHub repo. |

Only one prerequisite is enforced when enabling write-back:

1. **`remote_url`** must be set and point at GitHub, same as issue sync —
   write-back shells out to the `gh` CLI (same auth as import/PR sync).

Write-back is **independent of `issue_sync_enabled`** at the API/DB level —
you can enable one without the other. In practice they're used together,
since write-back only ever applies to tasks with a `source`/`source_ref`
(i.e. tasks the importer created); manually created tasks are never written
back to, even if they happen to reference an issue in their description.

Via the API:

```bash
curl -X PATCH http://localhost:8080/api/v1/repos/<id> \
  -H "Content-Type: application/json" \
  -d '{"issue_writeback_enabled": true}'
```

### Triggers

Three independent triggers fire the write-back actions below. Each is
best-effort: a failed `gh` call is logged and swallowed, never surfaced to
the human clicking a button or blocking a background sweep.

1. **Task leaves the human-gate label** (optional intermediate signal) — the
   first time a task's label moves off the workflow's human-gate label (the
   lowest `sort_order` `agent_ignore` label — `not_ready` in the default
   workflow), agent- or human-triggered, the source issue gets a label via
   `gh issue edit --add-label <label>`. The label is **configurable per
   repo** via `repos.issue_writeback_label` (`PATCH /repos/{id}` with
   `{"issue_writeback_label": "..."}`, or the "In-progress label" field on
   the Repos page under the "Issue write-back" checkbox); an unset/blank
   value falls back to the default `agent-in-progress`
   (`writeback.InProgressLabel`). Either way, the label — default or
   custom — **must already exist on the GitHub repo**: if it doesn't, the
   `gh` call fails — this is logged and ignored, and (unlike the two
   triggers below) is **not retried**: this is explicitly the optional
   signal, and retrying a call that's already failed on every future
   sweep/transition forever is worse than an occasional missed label.
2. **PR opened** — the first time a task gets a non-empty `pr_url`, the
   source issue gets a comment linking the PR
   (`gh issue comment --body "..."`).
3. **PR merged** — the first time a task's `git_state` becomes `pr_merged`,
   the source issue is closed with a comment linking the merged PR
   (`gh issue close --comment "..."`).

Triggers #2 and #3 fire from every code path that can move a task into that
state: the background `ghsync` PR-status sweep, the `POST
/tasks/{id}/pr` (`CreatePR`) and `POST /tasks/{id}/github-status`
(`GitHubStatus`) handlers, and — for the merged trigger only — the manual
`PATCH /tasks/{id}/git-state` (`UpdateGitState`) override, since a human
marking a task `pr_merged` by hand should still close the issue. Both are
safe to call unconditionally on every state (re)write, since the underlying
DB flag (see below) makes repeat calls a no-op; a failed `gh` call leaves the
flag unset, so the next sweep or handler call naturally retries.

### Idempotency

What's already been written back is tracked on the **task row**, not by
scraping the issue's existing comments — three flags
(`writeback_in_progress_sent`, `writeback_pr_commented`, `writeback_closed`)
that each get set once their corresponding action has succeeded and never
reset. This is cheaper than an extra `gh issue view --comments` call on every
sweep, and survives a human editing or deleting the marker comment on
GitHub.

Because the flags live on the task row, **deleting an imported task and
letting it re-import** (see the Deduplication section above) resets them —
the new task row starts with all three flags unset. If the source issue
already carries an old marker comment from the deleted task, a second PR
against the re-imported task can produce a duplicate-looking comment/label
sequence on the same GitHub issue. This is a known, accepted edge case, in
the same spirit as the existing re-import caveat above — avoid deleting
imported tasks with write-back enabled once they have PR activity.

Comments posted by this feature include an HTML comment marker
(`<!-- agent-task-editor:writeback -->`) so a human glancing at the issue can
tell an agent-task-editor write-back apart from their own comments. This
marker plays no role in idempotency (which is DB-flag based, not
comment-scraping based) — it's purely for human legibility.

## PR review / GitHub Actions feedback ingestion

This is the reverse direction of the loop above: instead of writing task
status *to* GitHub, `internal/ghsync`'s sweep reads GitHub PR reviews, inline
review comments, check-run results, and merge-conflict status back *into* the
task, for any task with a branch and an open PR — not just imported ones.

On every sweep, for each task with a resolved PR number, the syncer:

1. **Fetches inline review comments** (`gh api repos/{repo}/pulls/{n}/comments`,
   paginated) and inserts any not already ingested (deduped by the GitHub
   comment id) as `task_review_comments` rows tagged `source: "github"`,
   `external_id: "<github comment id>"`. These flow through the exact same
   path as comments left in-app: the `OPEN REVIEW COMMENTS` prompt section on
   the next dispatch, and the MCP `resolve_comment` tool / API resolve
   endpoint to close them out.
2. **Fetches reviews** (`gh pr view --json reviews`) and, for any
   `CHANGES_REQUESTED` review submitted after the last-seen cursor, appends
   its body to a feedback block.
3. **Fetches failed/cancelled checks** (`gh pr checks --json name,link,bucket`)
   and, if the set of failing check names differs from what was last
   surfaced for the current head commit, appends their names/links to the
   same feedback block.
4. **Checks whether the PR still merges cleanly** into its base branch
   (`gh pr list --json mergeable,baseRefName`, folded into the head-SHA call
   the ingestion cursor already makes, so it costs no extra `gh` invocation)
   and appends a resolve-the-conflict note to the same block the first time a
   conflict is seen. See "Merge conflict detection" below.
5. If that combined block is non-empty, it's **appended** (not overwritten)
   to the task's current agent run's `Feedback` column — read-modify-write,
   so it never clobbers a note a human already left via Reject. This is the
   same column rendered under the `FEEDBACK FROM PRIOR REVIEW:` prompt
   section on the run's next dispatch.

### Tracking / fresh-cycle-on-push

A per-task row in `task_pr_review_state` tracks a cursor (last-seen review
submission timestamp, a fingerprint of the last-surfaced failing checks, the
head commit + base branch a conflict was last surfaced at) plus the PR's head
commit SHA as of the last sweep. Re-sweeps only surface
reviews/checks newer than the cursor, so feedback is never duplicated across
sweeps.

When the PR's head SHA changes — i.e. the agent pushed a new commit — the
cursor resets, so reviews/checks against the new commit start a fresh
feedback cycle rather than being silently suppressed by the old cursor.
Already-ingested inline review comments are **not** purged or reset on a
push; they stay wherever they are until resolved (matching how locally-left
open review comments already behave across runs).

Every fetch is best-effort and independent: a `gh` failure fetching reviews,
say, is logged and swallowed and does not prevent comments or checks from
still being ingested that sweep, mirroring the write-back error-handling
style above.

### Merge conflict detection

A PR that merged cleanly when it was opened can start conflicting later
without the task's branch changing at all — someone else merged something
into the base branch first. Because the sweep re-reads every non-terminal
PR-bearing task on each interval, it notices:

- **The task row** carries `pr_mergeable`: `mergeable`, `conflicting`,
  `unknown`, or empty before the first check. It's written only when the
  verdict changes, and each change publishes a
  [`task.pr_mergeable_changed`](websocket.md#taskpr_mergeable_changed) WS
  event; the board and task detail views render a conflict marker from it.
  `GET /tasks/{id}/github-status` refreshes it on demand too.
- **The agent** gets a feedback line telling it to update the branch against
  the latest base branch, resolve the conflicted files, and push.

`unknown` is normal and transient rather than an error: GitHub computes the
test merge asynchronously after every push to either branch, so a freshly
pushed PR reports `UNKNOWN` for a few seconds. Nothing is surfaced to the
agent on an `unknown` verdict, and the conflict cursor is left alone — that
way a verdict flapping `conflicting -> unknown -> conflicting` doesn't read
as a brand-new conflict each time.

The feedback is deduped on `"<head sha>|<base ref>"`, so:

| Situation | Surfaced again? |
|---|---|
| Same conflict, next sweep | No — already told the agent |
| Agent pushed, still conflicting | Yes — the resolution attempt failed |
| PR retargeted to a different base, still conflicting | Yes |
| Went `mergeable`, then conflicts again at the same commit | Yes — the cursor is cleared on a clean verdict |
| PR merged or closed while conflicting | No — the verdict is recorded, but a closed PR is nobody's problem |

### Auto-transition (opt-in)

| Repo field | Meaning |
|---|---|
| `pr_review_auto_transition_enabled` | `1` to auto-move a task back to work when new PR feedback lands |

By default, newly-ingested PR feedback is surfaced in the prompt but the task
stays wherever a human put it — someone still has to click Reject (or
whatever the workflow's manual transition is) to send it back to an agent.
Setting `pr_review_auto_transition_enabled: 1` on the repo skips that click:
the first time a sweep ingests new feedback for a task (a changes-requested
review, a new inline comment, a newly-failing check, or a newly-detected merge
conflict), the task is
transitioned along its workflow's "failure" human-transition path — the same
destination label a manual Reject would use. If no such transition is
defined from the task's current label, or the transition is otherwise
invalid (e.g. the task moved concurrently), this is logged and skipped; a
human can always still transition the task by hand. Requires `remote_url`,
same as issue write-back.

Via the API:

```bash
curl -X PATCH http://localhost:8080/api/v1/repos/<id> \
  -H "Content-Type: application/json" \
  -d '{"pr_review_auto_transition_enabled": true}'
```

### v2 (not yet implemented)

Resolve/reply write-back — after the agent addresses an ingested GitHub
review comment (e.g. via the `resolve_comment` MCP tool), replying on the
originating GitHub review thread — is intentionally out of scope for v1. The
`external_id`/`source` columns on `task_review_comments` are structured to
make that a natural follow-up without a schema change.
