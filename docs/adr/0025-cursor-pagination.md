# 25. List endpoints use id-cursor pagination with a plain-array body

Status: Accepted

## Context

Task, run, log and config lists grow without bound. The frontend (stores,
dropdowns) assumes plain arrays, and a task's lifetime cost cannot be summed
from a single page of runs (issue #261).

## Decision

Newest-first cursor pagination: tasks and config lists on `(created_at, id)`,
logs on `(timestamp, id)`; `id` is a tie-breaker so the order is total. The
cursor is the last row's id; the query resolves that row's own timestamp so the
comparison matches `ORDER BY` regardless of timestamp text format. Handlers
fetch `limit+1`, return a plain array, and put the next cursor in
`X-Next-Cursor` (absent on the last page). Defaults: tasks/configs 200 (cap
500), runs 100 (cap 500), logs 200 (cap 1000; logs use `?before=`,
`X-Has-More` and `X-Prev-Cursor`, body chronological). `GET /agents` pages
all configs; the internal enabled-only lookup stays unpaginated, as do
`ListIssueSyncRepos`. `Task.cumulative_cost_usd` (via `SumTaskCost`) replaces
client-side summing. `workflows` List pages raw rows before the label and
transition fan-out. The frontend's `listAllPages()` loops on `X-Next-Cursor` so
callers still receive `Promise<T[]>`.

## Consequences

- New list endpoints should follow this shape and use positional params
  (ADR 0020).
- Derived badge data (dependency counts, subtask rollups) is queried scoped to
  the returned ids (`sqlc.slice`), not whole-table scans.
