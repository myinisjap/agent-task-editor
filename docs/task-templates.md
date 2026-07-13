# Task Templates & Recurring Schedules

**Task templates** are reusable pre-filled title/description/type snippets
for recurring shapes of work — "upgrade dependency X", "triage flaky
tests", "update the changelog". They pre-fill the new-task form so a human
doesn't have to retype the same task boilerplate every time.

**Task schedules** turn a template into a fully automated cron job: fire the
template as a new task on a repo, on a schedule, without any human clicking
"new task".

## Task templates

Managed on the **Templates** page in the UI, or via the API:

```bash
curl -X POST http://localhost:8080/api/v1/templates \
  -H "Content-Type: application/json" \
  -d '{"name": "Upgrade dependency", "title": "Upgrade <package> to latest", "description": "Bump the version, run tests, note breaking changes.", "type": "chore"}'
```

| Field | Meaning |
|---|---|
| `name` | Unique display name |
| `title` / `description` / `type` | Pre-filled into the new-task form (or a schedule's created task) |

`name` is unique; deleting a template also deletes any schedules that
reference it (`ON DELETE CASCADE`).

## Recurring schedules

A `task_schedule` links a template to a repo and a cron expression. A
background sweep (`internal/schedule`, same poll-loop shape as the GitHub
Issues importer) checks every enabled schedule and creates a task from its
template whenever the schedule is due.

```bash
curl -X POST http://localhost:8080/api/v1/schedules \
  -H "Content-Type: application/json" \
  -d '{"template_id": "<template id>", "repo_id": "<repo id>", "cron_expr": "0 6 * * 1", "target_label": "not_ready", "enabled": true}'
```

| Field | Meaning |
|---|---|
| `template_id` | Which template to instantiate |
| `repo_id` | Which repo the created task belongs to (must have a workflow assigned) |
| `cron_expr` | Standard 5-field cron: `minute hour day-of-month month day-of-week`. Supports `*`, single values, comma lists, and `*/N` steps. |
| `target_label` | The label the created task starts on. Default `not_ready`. |
| `enabled` | Whether the schedule fires at all |

The UI's schedule editor (embedded in the Templates page, per template)
offers a few presets that resolve to a `cron_expr`:

| Preset | Cron |
|---|---|
| Hourly | `0 * * * *` |
| Daily at 06:00 | `0 6 * * *` |
| Weekly on Monday at 06:00 | `0 6 * * 1` |
| Custom | any raw cron expression |

### Deduplication — never stacking on an unfinished run

Before firing, the sweep checks whether an **open** task from a prior firing
of the same schedule still exists, and skips creating a new one if so. "Open"
means: not archived, and not sitting on a workflow label marked
`is_terminal`. This is the same "done-ness" predicate used elsewhere (e.g.
task dependency satisfaction) — so a weekly "upgrade deps" schedule doesn't
pile a second task on top of last week's still-open one, but does fire again
promptly once the prior task reaches a terminal label or is archived.

Every firing creates a task with `source = "schedule"` and
`source_ref = "<schedule id>#<run marker>"` (a schedule fires repeatedly, so
each task needs a distinct `source_ref` even though they all trace back to
one schedule).

### `not_ready` vs. a live agent label — the unattended mode

By default `target_label` is `not_ready`, matching how manually created and
imported tasks start: a human reviews and promotes the task into the
workflow. Setting `target_label` to a label the workflow's dispatcher picks
up directly (an "agent" trigger-type label) instead means the created task
is **immediately picked up by an agent** — no human in the loop at all.

This is intentional and enables fully unattended maintenance loops (e.g. a
weekly "upgrade dependencies" schedule that runs end-to-end with no human
involvement). Because it removes human review as a safety net, pair it with
a **cost budget** (`max_cost_usd`) on the target agent config, so a runaway
or repeatedly-failing unattended run can't burn unbounded spend. The UI
flags this combination with a warning when a non-`not_ready` target label is
entered.

## Polling interval

The sweep runs on a fixed interval, configurable via the `SCHEDULE_INTERVAL`
env var / `schedule_interval` YAML key (Go duration syntax, default `30s`).
Cron expressions are minute-granularity, so this only needs to be frequent
enough to reliably catch each minute boundary.
