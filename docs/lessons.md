# Repo Lessons

Lessons are short, human-curated pieces of knowledge that carry **across tasks**.
Everything else an agent sees is per task (`update_task_notes`, human decisions,
feedback). A lesson such as "run `make test`, not `go test ./...`" is stored once
and injected into later agent prompts for that repo.

## Fields

| Field | Meaning |
|---|---|
| `kind` | `convention`, `trap`, `command`, `decision` or `preference` |
| `body` | The lesson text, at most 300 characters |
| `path_globs` | Empty = always applies. Otherwise the lesson applies only when the task branch changed a matching file. Supports `**`, `*`, `?`; a pattern without `/` matches the basename (`*.sql`); a trailing `/` means a directory prefix |
| `stages` | Workflow labels (e.g. `work`, `review`) the lesson applies to. Empty = all agents |
| `status` | `proposed`, `active`, `stale` or `retired`. Only `active` lessons are injected |
| `source` | `human` (default), `agent` or `distiller` |
| `citations` | JSON array, reserved for later citation checks |
| `source_task_id`, `injected_count`, `last_checked_at` | Provenance and bookkeeping |

## Scope

A lesson with a `repo_id` applies to that repo. A lesson with `repo_id` null is
**global** and applies to every repo. Deleting a repo deletes its lessons.

## Injection

When a run starts, the dispatcher picks the repo's active lessons plus the
global ones and keeps those that pass:

- **Stage filter:** `stages` is empty or contains the run's current label.
- **Glob filter:** `path_globs` is empty, or some file changed by the task branch
  (diff against its merge-base with the base ref) matches a glob. With no diff
  (for example a fresh worktree) lessons that have globs are skipped.

The survivors are ordered global and stage-specific first, then newest first,
and capped at **15 lessons / about 2,000 characters**. A lesson that does not fit
is skipped and a later, shorter one may still be included. Each injected lesson
gets `injected_count` incremented. Selection runs in the pool worker after the
worktree is set up, never in the dispatcher sweep.

They appear in the prompt after HUMAN DECISIONS and before NOTES FROM PRIOR AGENT:

```
REPO LESSONS (conventions learned from past tasks in this repo; if one contradicts the code, trust the code and say so in your notes):
- [trap] ...
- [command] ...
```

Resumed provider sessions do not get the section (the conversation already has
it) and do not increment `injected_count`. Lessons can go stale, so agents are
told to trust the code over a lesson and to say so in their notes.

## Distiller-proposed lessons

An offline distiller can draft lessons for a repo, following the rule-proposal
pattern (ADR 0040): **a model may only propose.** A draft is a normal lesson with
`status=proposed` and `source=distiller`; only `active` lessons are injected, so
a draft does nothing until a person accepts it.

- **Evidence** (last 30 days, per repo, at most 40 items): human rejection notes at gates, human replies to agent questions, failure feedback that caused rework (agent review, tester, verify), tasks whose verify command failed at least twice, and "should have asked" notes. Moves made by the `pr-feedback-sync` and `policy` actors are not human decisions and are excluded. All of it is fenced as untrusted content (ADR 0014).
- **Model:** the provider config chosen for rule proposals (`proposal_provider_config_id`) and its interval (`proposal_interval_hours`; 0 = on demand only). It runs in an empty scratch directory with shell commands denied; each run records status and cost.
- **Validation before storing:** the same rules as a human-created lesson (kind, body <= 300 characters, globs and stages), near-duplicates of existing active, proposed, stale or retired lessons (repo and global) and of dismissed proposals are dropped, candidates that cite no real task from the evidence are dropped, and at most 5 are kept per run. `source_task_id` and `citations` (task, title, kind, quoted note) are filled in.
- **Accept** with `PATCH /lessons/{id}` `{"status":"active"}` (optionally with edits). **Dismiss** with `POST /lessons/{id}/dismiss`: the proposal is deleted and its text remembered, so it is not offered again.
- **When it runs:** on demand ("Suggest lessons" on the repo's Lessons panel, `POST /repos/{id}/lessons/distill`) and on the interval, only for repos with new evidence since the last run.
- **UI:** the repo's Lessons panel has a **Suggested** section with each proposal, its linked tasks and quoted notes, Accept / Edit and accept / Dismiss, and the last run time and cost. Proposals are not counted in Needs you.

## UI

- **Repos page** — each repo has a **Lessons** button that opens a panel to add, edit, retire/reactivate and delete lessons, filter by status, and see each lesson's kind, body, scope (path globs, stages) and injected count.
- **Settings → Lessons** — the same controls for global (repo-less) lessons.
- **Task Detail → Remember for this repo** — shown next to each human reply (the "Human decisions" list, backed by `GET /tasks/{id}/human-decisions`) and each human label-history note (e.g. rejections). It opens a form prefilled with that text (trimmed to 300 characters, kind `preference`) and creates an active lesson with `source_task_id` set to the task.
- **Should have asked** — `POST /tasks/{id}/autonomy/should-have-asked` can create an active lesson (kind `trap` by default) whose path globs are the ask rule's globs, with `source_task_id` set to the task. See [workflows.md](workflows.md#should-have-asked).


## API

```bash
# repo lesson
curl -X POST $ATE/api/v1/repos/$REPO/lessons -H "Authorization: Bearer $TOKEN" \
  -d '{"kind":"command","body":"Run make test, not go test ./...","stages":["work","review"],"path_globs":["backend/**"]}'
# global lesson
curl -X POST $ATE/api/v1/lessons -H "Authorization: Bearer $TOKEN" \
  -d '{"kind":"preference","body":"Keep PRs small."}'
# retire
curl -X PATCH $ATE/api/v1/lessons/$ID -H "Authorization: Bearer $TOKEN" -d '{"status":"retired"}'
```

See [api.md](api.md#repo-lessons).

Task runs also keep Claude Code's own auto-memory off, see [providers/claude.md](providers/claude.md#auto-memory).
