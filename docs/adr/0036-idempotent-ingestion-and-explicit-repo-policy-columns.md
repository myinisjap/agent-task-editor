# 36. External ingestion is idempotent by external id; repo policy columns are always written explicitly

Status: Accepted

## Context

The GitHub Issues importer, PR review ingestion and schedule firings all
re-observe the same upstream items every sweep. The importer began create-only
(deduped by `CountTasksBySource`, which counted rather than fetched, so it had
no upstream row to diff against), and later became a two-way sync (migration
044). Separately, SQLite column defaults apply only to INSERTs that omit the
column, and `handlers/repos.go` names every field, so a handler that passed
zero values would write `''`/`0` and, on PATCH, wipe a configured policy.

## Decision

- Look up before insert and back it with a unique index: tasks by
  `(source, source_ref)` partial index (`GetTaskBySource` then
  `CreateSourcedTask`, migration 022); review comments by
  `(task_id, external_id)` partial index (migration 040); source comments by
  unique `(task_id, external_id)` (migration 044). Re-ingesting is a no-op.
- Per-task ingestion cursors live in `task_pr_review_state` (head sha, last
  review time, last failed check sha, last conflict `"<head sha>|<base ref>"`)
  so a persisting condition (e.g. a merge conflict) is not re-injected as
  feedback every sweep. Inline comment dedup uses the unique index instead of a
  cursor.
- Sync policy is per repo: `issue_sync_update_policy` (`gate`|`always`|`never`),
  `issue_sync_gone_action` (`flag`|`archive`|`move`), `issue_sync_gone_label`,
  `issue_comment_sync_enabled`; `tasks.source_state` (`''`|`gone`) records
  upstream absence.
- `handlers/repos.go` resolves `''` to documented defaults
  (`resolveIssueSyncUpdatePolicy`/`resolveIssueSyncGoneAction`) and
  `repos_test.go` pins that an unrelated PATCH preserves all four policy
  fields. PATCH merges: omitted fields keep their values. Enabling issue sync
  requires `remote_url` and `workflow_id`.
- `ListGhSyncEligibleTasks` filters in SQL (branch set, not archived, git state
  not merged/closed) so `gh` calls per sweep stay bounded by open work.
- Background polls must not make a task look edited: `SetTaskPRMergeable`
  leaves `updated_at` alone, and `SetTaskPR` callers preserve an existing URL
  when the live query surfaces none.

## Consequences

- New ingestion sources need an external-id unique index and a lookup-first
  insert.
- Adding a repo column means updating create/update params and its default
  resolution, and adding a PATCH-preservation test.
