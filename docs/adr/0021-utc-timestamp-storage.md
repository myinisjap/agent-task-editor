# 21. All timestamps are stored as UTC `YYYY-MM-DD HH:MM:SS`

Status: Accepted

## Context

SQLite stores timestamps as text. Mixed formats (`CURRENT_TIMESTAMP` vs Go's
RFC3339 with zones) made string comparison and `ORDER BY` wrong and forced
`datetime()` wrapping in queries.

## Decision

`storage.Open` registers a thin wrapper around mattn/go-sqlite3 that converts
every bound `time.Time`/`*time.Time` to this format via `storage.FormatTime`
(sub-second precision dropped). Migration 058 rewrote legacy rows. TEXT cursor
columns parsed as RFC3339 in Go (`task_pr_review_state.last_*`) and
`task_source_comments.external_created_at` are intentionally not canonicalized.

## Consequences

- Compare and order timestamp columns as plain strings; do not wrap them in
  `datetime()`.
- Build hand-made timestamp strings with `storage.FormatTime(t)`.
