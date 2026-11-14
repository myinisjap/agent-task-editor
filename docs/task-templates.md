# Task Templates & Recurring Schedules

**Task templates** are reusable pre-filled title/description/type snippets
for recurring shapes of work — "upgrade dependency X", "triage flaky
tests", "update the changelog". They pre-fill the new-task form so a human
doesn't have to retype the same task boilerplate every time.

**Task schedules** turn a template into a fully automated cron job: fire the
template as a new task on a repo, on a schedule, without any human clicking
"new task".

If you just want to re-run a *specific* task with small tweaks — a run that
half-worked, or the same change applied to a second repo — use **Duplicate**
(on the task detail page or a board card) instead of a template: it pre-fills
the new-task form from that one task rather than a saved reusable shape.

## Task templates

Managed on the **Settings → Templates** page in the UI (`/settings/templates`), or via the API:

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
| `target_label` | The label the created task starts on. Defaults to the repo workflow's human-gate label (the lowest `sort_order` `agent_ignore` label, or the first label if none — `not_ready` in the default workflow). Must be one of the repo's workflow's labels — `POST`/`PUT` reject an unrecognized label with `400`. |
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

### Human-gate label vs. a live agent label — the unattended mode

By default `target_label` is the workflow's human-gate label (`not_ready` in
the default workflow), matching how manually created and imported tasks
start: a human reviews and promotes the task into the workflow. Setting
`target_label` to a label the workflow's dispatcher picks up directly (an
"agent" trigger-type label) instead means the created task is **immediately
picked up by an agent** — no human in the loop at all.

This is intentional and enables fully unattended maintenance loops (e.g. a
weekly "upgrade dependencies" schedule that runs end-to-end with no human
involvement). Because it removes human review as a safety net, pair it with
a **cost budget** (`max_cost_usd`) on the target agent config, so a runaway
or repeatedly-failing unattended run can't burn unbounded spend. The UI
flags this combination with a warning when a target label other than the
default human-gate label is entered.

## Polling interval

The sweep runs on a fixed interval, configurable via the `SCHEDULE_INTERVAL`
env var / `schedule_interval` YAML key (Go duration syntax, default `30s`).
Cron expressions are minute-granularity, so this only needs to be frequent
enough to reliably catch each minute boundary.

## Intake rules also apply to schedules

A firing schedule is matched against enabled [intake rules](task-sources.md#intake-routing-rules)
(`match_source: schedule`, matched against the template's own title/body) the
same way an imported issue is. A matching rule can supply `apply_priority`
and `apply_max_cost_usd` unconditionally, and can supply `apply_target_label`
— but **only when the schedule's own `target_label` is left empty**. A
schedule's explicit `target_label` is a human-set, already-validated value
and always takes precedence over a rule's; a rule only fills in the label
when the schedule doesn't specify one itself. This lets a rule provide a
shared default target label across many schedules while still letting any
individual schedule override it.

Unlike issue import, a matched rule's `apply_target_label` is **not** subject
to the [auto-start safety gate](task-sources.md#the-auto-start-safety-gate):
a schedule's target label is operator-authored configuration, not untrusted
imported content, so the mitigation that gate exists for doesn't apply here.

## Automation recipes

Settings → Intake rules → **Recipes** is a gallery of one-click presets that create the
right template, schedule and/or webhook endpoint in one step. Recipes are declarative
definitions bundled in the backend (`backend/internal/recipes`) and versioned with the
app. They expand into ordinary objects — nothing here runs agents differently.

| Recipe id | Creates |
| --- | --- |
| `ci-red-main` | endpoint + rule + template "Fix failing CI on main" (CI webhook, deduped while main stays red) |
| `error-tracker-alert` | endpoint + rule + template for a Sentry-style issue webhook |
| `weekly-dependency-updates` | schedule + template (default `0 9 * * 1`) |
| `stale-todo-flaky-sweep` | schedule + template (default `0 9 * * 3`) |
| `docs-drift-check` | schedule + template (default `0 9 * * 5`) |

API: `GET /api/v1/automation-recipes` lists recipes, their parameters (`repo_id`,
`workflow_id`, `target_label`, `cron_expr`, `branch`) and installed state;
`POST /api/v1/automation-recipes/{id}/install` with `{"params": {...}, "confirm_auto_start": false}`
creates everything in one transaction; `DELETE /api/v1/automation-recipes/{id}` removes the
recipe. The Board MCP `admin` profile has `list_recipes` / `install_recipe` (no remove tool).

- **Tagged objects.** Every created template, schedule, endpoint and rule carries
  `recipe_id`; the UI shows "from recipe" and **Remove recipe** deletes exactly the tagged
  objects (tasks already created are kept). A recipe can be installed once at a time; a
  template with the same name that you made yourself blocks the install (409).
- **Parked by default.** An empty `target_label` means tasks land on the workflow's
  human-review (gate) label for you to promote — for schedule recipes that is the same default
  as the schedules API. Picking an agent-triggerable label is rejected with
  `auto_start_confirmation_required` unless `confirm_auto_start` is true, and the result
  carries the same warning schedules and intake rules show.
- **Existing limits apply.** Webhook recipes use ordinary intake endpoints: signature check,
  256 KB body cap, 30 events/minute/endpoint, and dedupe via the endpoint's dedupe key.
- **Webhook recipes** return the endpoint URL and secret **once**, plus a paste-ready sender
  snippet (the snippet never contains the secret; store it as `ATE_INTAKE_SECRET`).

See [task-sources.md](task-sources.md#recipe-webhooks) for the payload shapes and the GitHub
Actions step.
