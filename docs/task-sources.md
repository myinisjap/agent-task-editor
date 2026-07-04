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

## Roadmap (v2)

Status write-back is intentionally out of scope for v1: commenting on the
issue when the task's PR opens, and closing the issue when `git_state`
reaches `pr_merged`. The `source`/`source_ref` fields exist so this can be
added without schema changes.
