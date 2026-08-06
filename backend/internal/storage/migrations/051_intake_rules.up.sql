-- Intake routing rules: a small match->apply table evaluated at
-- task-creation time for the 'issue' and 'schedule' sources (v1 — see
-- docs/task-sources.md). Subsumes the single-string issue_sync_label filter,
-- which becomes a degenerate one-rule case (see the data migration below).
--
-- Rules are evaluated first-match-wins in (enabled, sort_order, created_at)
-- order — see internal/intake for the matcher and its documented semantics.
--
-- MATCH columns: empty string / '[]' means "no constraint" (matches
-- anything) for that dimension. match_labels and match_author_assoc are
-- JSON arrays of strings, matched ANY-of. match_title_pattern/
-- match_body_pattern are Go regexps (compiled and validated by the CRUD
-- handler at write time).
--
-- APPLY columns are all optional (NULL/''/unset = "leave the caller's
-- default"), letting a rule shape only the fields it cares about.
--
-- apply_target_label landing a task on an agent-triggerable (non-
-- agent_ignore) label bypasses the human-review gate that protects against
-- untrusted imported issue content (see #331) — the CRUD handler enforces
-- that such a rule must also carry a match_author_assoc restricted to
-- trusted associations (OWNER/MEMBER/COLLABORATOR); see
-- intake.AutoStartAllowed, the single place this is enforced.
CREATE TABLE intake_rules (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    enabled             BOOLEAN NOT NULL DEFAULT 1,
    sort_order          INTEGER NOT NULL DEFAULT 0,
    -- MATCH
    match_source        TEXT NOT NULL DEFAULT '',   -- '' = any; else manual|issue|schedule|subtask
    match_repo_id       TEXT REFERENCES repos(id) ON DELETE CASCADE,  -- NULL = any repo
    match_labels        TEXT NOT NULL DEFAULT '[]', -- JSON array; ANY-of, case-insensitive
    match_title_pattern TEXT NOT NULL DEFAULT '',   -- Go regexp, '' = any
    match_body_pattern  TEXT NOT NULL DEFAULT '',   -- Go regexp, '' = any
    match_author_assoc  TEXT NOT NULL DEFAULT '[]', -- JSON array of OWNER|MEMBER|COLLABORATOR|CONTRIBUTOR|NONE; [] = any
    -- APPLY
    apply_template_id   TEXT REFERENCES task_templates(id) ON DELETE SET NULL,
    apply_priority      INTEGER,                    -- NULL = leave default
    apply_target_label  TEXT NOT NULL DEFAULT '',
    apply_workflow_id   TEXT REFERENCES workflows(id) ON DELETE SET NULL,
    apply_max_cost_usd  REAL,                        -- NULL = leave default
    stop_processing     BOOLEAN NOT NULL DEFAULT 1,  -- reserved; first-match-wins is the only mode in v1
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_intake_rules_order ON intake_rules(enabled, sort_order);

-- Records which rule (if any) shaped a task at creation, so "why did this
-- task land here with this priority" is answerable from the task itself
-- rather than requiring rule-table archaeology (see #353's complaint about
-- invisible automated decisions).
ALTER TABLE tasks ADD COLUMN matched_rule_id TEXT REFERENCES intake_rules(id) ON DELETE SET NULL;

-- Data migration: convert existing per-repo issue_sync_label scalars into
-- equivalent rules, so the two intake-matching mechanisms don't coexist
-- indefinitely (see the issue_sync_label deprecation note in
-- docs/task-sources.md). issue_sync_label remains a *fetch* filter for one
-- more release (narrows the API query), while the generated rule only
-- shapes the resulting tasks — it does not change which issues are
-- imported. Rows whose label contains a double-quote or backslash are
-- skipped here (can't be safely JSON-encoded in pure SQL); the application
-- logs a startup warning for any such repo instead so it isn't silently
-- dropped.
INSERT INTO intake_rules (id, name, enabled, sort_order, match_source, match_repo_id, match_labels)
SELECT
    lower(hex(randomblob(16))),
    'Imported from ' || name || ' issue sync',
    1,
    0,
    'issue',
    id,
    '["' || issue_sync_label || '"]'
FROM repos
WHERE issue_sync_enabled != 0
  AND issue_sync_label != ''
  AND instr(issue_sync_label, '"') = 0
  AND instr(issue_sync_label, char(92)) = 0;
