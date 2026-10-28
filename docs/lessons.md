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

## UI

- **Repos page** — each repo has a **Lessons** button that opens a panel to add, edit, retire/reactivate and delete lessons, filter by status, and see each lesson's kind, body, scope (path globs, stages) and injected count.
- **Settings → Lessons** — the same controls for global (repo-less) lessons.
- **Task Detail → Remember for this repo** — shown next to each human reply (the "Human decisions" list, backed by `GET /tasks/{id}/human-decisions`) and each human label-history note (e.g. rejections). It opens a form prefilled with that text (trimmed to 300 characters, kind `preference`) and creates an active lesson with `source_task_id` set to the task.

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

See [api.md](api.md#repo-lessons). LLM-proposed lessons (with review-queue
approval) come in a later phase.

Task runs also keep Claude Code's own auto-memory off, see [providers/claude.md](providers/claude.md#auto-memory).
