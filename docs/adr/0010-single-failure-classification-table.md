# 10. One classification table for provider failures

Status: Accepted

## Context

Whether a failure is retryable (rate limit, network blip, 5xx), needs a human
(not logged in), or is a genuine task failure drives retry budgets and
escalation. String sniffing scattered across providers produced
misclassifications that were hard to diagnose, and short patterns (`429`,
`eof`, `timeout`) false-positived on ordinary agent output such as diff hunk
headers (`@@ -429,7 +429,9 @@`) or `typeof` in source (issue #335).

## Decision

`agent/errclass.go` is the single table: `Classification` is one of `genuine`,
`transient`, `rate_limit`, `auth`, plus the structurally detected `max_turns`
and `cost_budget`. `ClassifyLine` holds the patterns, each with unit tests.
Short patterns are anchored regexps (word boundary, and an HTTP-status-ish
context for 3-digit codes), not bare substrings. The classification is logged
as a field on the failure line.

- Typed errors win: anything implementing `Transient() bool`
  (`ErrRateLimit`, `ErrTransient`) is transient; HTTP providers wrap network
  errors and 5xx as `ErrTransient`, 429 as `ErrRateLimit`.
- For claude/qwen the typed stream-json `result` event is preferred over line
  sniffing; codex uses `classifyCodexJSON` for the same reason.
- Raw-line sniffing of **stdout** applies only to lines that did not parse as a
  structured event (`parsedJSON`/`streamEvent.Parsed`), since a parsed event's
  content is the agent's own prose or file contents. **Stderr** is always
  sniffed.
- A context-deadline timeout is transient. A plain non-zero exit or
  `Result{failed}` with no signal is genuine.
- opencode has no typed classification, so a rate limit that appears only inside
  a structured stdout event is not caught there; it still surfaces via stderr
  and exit code.

## Consequences

- A CLI wording change is a one-line edit with a test.
- `providers.is429Line`/`isTransientLine` stay thin wrappers over this table.
