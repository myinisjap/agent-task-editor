# Task Sources — GitHub Issues Import

Tasks are normally created by hand on the board, but they can also be
**imported from an external tracker**. v1 ships one source: **GitHub
Issues**. A background poller (`internal/tasksource`) sweeps every repo that
has issue sync enabled and creates a board task for each matching open issue.

## Enabling it

Issue sync is configured **per repo** (Repos page in the UI, or the REST
API):

| Repo field | Meaning |
|---|---|
| `issue_sync_enabled` | `1` to turn the importer on for this repo |
| `issue_sync_label` | Only import open issues carrying this label (e.g. `agent-ok`). Empty = **all** open issues |

Two prerequisites are enforced when enabling:

1. **`remote_url`** must be set and point at GitHub — issues are fetched with
   the `gh` CLI (same auth as PR sync: `gh auth login` or `GITHUB_TOKEN`).
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
- **label** — `not_ready`, same as manually created tasks, so nothing runs
  until a human moves the task into an agent-triggerable column
- **source / source_ref** — `github` / `owner/repo#123`, shown as a link back
  to the issue on the task detail page

Pull requests are never imported (`gh issue list` excludes them).

## Deduplication

`(source, source_ref)` is unique across tasks (enforced by a partial unique
index), and the importer skips any issue that already has a task —
regardless of what column that task has moved to since. Closing the issue or
finishing the task does not cause a re-import.

**Deleting** an imported task while its issue is still open and still
matches the filter *will* re-import it on the next sweep. To keep an issue
off the board, remove the filter label from the issue (or close it) instead
of deleting the task.

## Polling interval

The importer sweeps on a fixed interval, configurable via the
`ISSUE_SYNC_INTERVAL` env var / `issue_sync_interval` YAML key (Go duration
syntax, default `60s`).

## Status write-back

Alongside issue import, a repo can opt in to writing task status back to the
GitHub issue an imported task originated from — so people watching the
tracker get a signal that an agent is already working on it, without having
to check the board.

| Repo field | Meaning |
|---|---|
| `issue_writeback_enabled` | `1` to turn write-back on for this repo's imported tasks |

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

1. **Task leaves `not_ready`** (optional intermediate signal) — the first
   time a task's label moves off `not_ready` (agent- or human-triggered), the
   source issue gets an `agent-in-progress` label via
   `gh issue edit --add-label agent-in-progress`. This label name is
   currently **fixed, not configurable** per repo; a future request could add
   a per-repo custom label field, but v2 ships a sensible default. If the
   repo doesn't already have an `agent-in-progress` label defined, the `gh`
   call fails — this is logged and ignored, and (unlike the two triggers
   below) is **not retried**: this is explicitly the optional signal, and
   retrying a call that's already failed on every future sweep/transition
   forever is worse than an occasional missed label.
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
review comments, and check-run results back *into* the task, for any task
with a branch and an open PR — not just imported ones.

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
4. If that combined block is non-empty, it's **appended** (not overwritten)
   to the task's current agent run's `Feedback` column — read-modify-write,
   so it never clobbers a note a human already left via Reject. This is the
   same column rendered under the `FEEDBACK FROM PRIOR REVIEW:` prompt
   section on the run's next dispatch.

### Tracking / fresh-cycle-on-push

A per-task row in `task_pr_review_state` tracks a cursor (last-seen review
submission timestamp, a fingerprint of the last-surfaced failing checks) plus
the PR's head commit SHA as of the last sweep. Re-sweeps only surface
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

### Auto-transition (opt-in)

| Repo field | Meaning |
|---|---|
| `pr_review_auto_transition_enabled` | `1` to auto-move a task back to work when new PR feedback lands |

By default, newly-ingested PR feedback is surfaced in the prompt but the task
stays wherever a human put it — someone still has to click Reject (or
whatever the workflow's manual transition is) to send it back to an agent.
Setting `pr_review_auto_transition_enabled: 1` on the repo skips that click:
the first time a sweep ingests new feedback for a task (a changes-requested
review, a new inline comment, or a newly-failing check), the task is
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
