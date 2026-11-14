# Repo map

A repo map is a short, generated wiki of a codebase: what the project is, where
things live, how to build/test/run it, the key flows, and the conventions and
traps. It is stored per repo, refreshed as the default branch moves, shown on
the Repos page, and offered to planning agents so they start with orientation
instead of re-exploring the repo.

It is **off by default** and opt-in per repo.

## Pages

Fixed outline, in this order:

| Slug | Title |
|---|---|
| `overview` | Overview |
| `layout` | Layout (directories and what lives where) |
| `build-test-run` | How to build, test and run |
| `key-flows` | Key flows |
| `conventions` | Conventions and traps |

Hard caps: each page is at most 5,000 characters and a whole map at most 20,000.
Longer output is truncated (marked `_(truncated)_`). A run that does not produce
both `overview` and `layout` fails and stores nothing. Every page records the
commit SHA it was generated from.

If the repo already has `AGENTS.md`, `CLAUDE.md` or a README at its top level,
the Overview ends with a line pointing to them, and the generator is told to
point to them rather than restate them.

## Turning it on

On the Repos page click **Map** on a repo, choose a provider config, tick
**Enabled**, then **Generate**. The same via the API:

```
PUT  /api/v1/repos/{id}/map/config   {"enabled": true, "provider_config_id": "<id>"}
POST /api/v1/repos/{id}/map/generate
```

Config fields: `refresh_interval_hours` (default 168, i.e. weekly; 0 = manual
only) and `min_commits_behind` (default 20).

## How generation works

A run checks out the repo's default branch (`origin/HEAD`, else `origin/main`,
else `HEAD`; a best-effort `git fetch` runs first) into a **scratch detached
worktree** under the system temp dir, makes it read-only on disk, runs the
provider CLI there outside any task (no MCP sidecar, no task, no push), and
removes the worktree afterwards. The run is locked down with a command denylist
(`ReadOnlyDenylist` in `backend/internal/repomap`) that blocks writes (`rm`,
`mv`, `tee`, redirection, `sed -i`...), installs/builds, network commands and any
git command that changes state (`push`, `commit`, `checkout`, `worktree`...). Like
every command denylist it is best-effort string matching; the read-only checkout
and the absence of a task are the other layers. Providers that do not enforce
denylists (`codex_cli`) get only those other layers, so prefer `claude`.

Each attempt is recorded in `repo_map_runs` with its status, commit, page and
character counts, tokens and `cost_usd`. A run is also capped at $3 mid-run
for providers with a cost watchdog. Pages of the last 3 successful runs are
kept (`repo_map_pages`); older pages are pruned, run rows stay.

Global cost ceilings (`MAX_DAILY_COST_USD`, `MAX_MONTHLY_COST_USD`) are
respected: while one is tripped no run starts (manual requests get 409,
the scheduler skips). Map runs are not agent runs, so their cost is shown on the
run but is not counted toward those ceilings.

## Refresh

- **On demand:** the Refresh button / `POST /repos/{id}/map/generate`.
- **On an interval:** once per `refresh_interval_hours` per enabled repo, and
  only if the map is stale: no map yet, or the default branch is more than
  `min_commits_behind` commits ahead of the recorded SHA, or the set of
  top-level files/directories changed since it (and the branch moved at all), or
  the recorded SHA no longer exists. A failed run also waits out the interval.

The UI shows "generated at `<sha>`, N commits behind".

## Injection into agent prompts

For runs on labels flagged for it, the prompt gets a bounded `REPO MAP` section:
the Overview and Layout pages, at most about 2,000 characters in total, with the
generated-at SHA and a note that it may be stale. It is fenced as generated
content, placed above the task, and states that it never overrides the task, the
human decisions or the repo's own docs. It is omitted when the repo has no
enabled map, no successful run yet, the label is not flagged, or the agent
config opted out. Resumed sessions do not get it again.

Global settings (`GET`/`PUT /api/v1/settings/repo-map`):

| Field | Default | Meaning |
|---|---|---|
| `inject_labels` | `["plan"]` | Workflow labels whose runs get the section (planning only by default) |
| `excluded_agent_ids` | `[]` | Agent configs that opted out |

## Board MCP

`get_repo_map` (in the `readonly` profile and above) returns the pages,
the SHA and staleness for a repo.

## Out of scope / follow-ups

- Free-form "ask the codebase" Q&A (the Chat tab covers interactive questions).
- A UI for `inject_labels` / `excluded_agent_ids` (API only today).
- Counting map-run cost toward the global cost ceilings.
