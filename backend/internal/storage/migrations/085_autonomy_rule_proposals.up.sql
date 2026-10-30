-- ADR 0040: a model may propose ask rules from a gate's escapes and
-- rejections. Proposals are inert until a person accepts one, which creates a
-- normal autonomy_ask_rules row. No REFERENCES on the new settings column:
-- SQLite cannot DROP COLUMN an FK column in the down.
ALTER TABLE autonomy_settings ADD COLUMN proposal_provider_config_id TEXT;
ALTER TABLE autonomy_settings ADD COLUMN proposal_interval_hours INTEGER NOT NULL DEFAULT 24;

CREATE TABLE autonomy_proposal_runs (
    id                 TEXT PRIMARY KEY,
    workflow_id        TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    label              TEXT NOT NULL,
    trigger            TEXT NOT NULL CHECK (trigger IN ('schedule','manual')),
    actor              TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','skipped')),
    provider_config_id TEXT,
    evidence_count     INTEGER NOT NULL DEFAULT 0,
    proposal_count     INTEGER NOT NULL DEFAULT 0,
    flag_ideas         TEXT NOT NULL DEFAULT '[]',
    error              TEXT NOT NULL DEFAULT '',
    cost_usd           REAL NOT NULL DEFAULT 0,
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    started_at         DATETIME NOT NULL,
    finished_at        DATETIME
);
CREATE INDEX idx_autonomy_proposal_runs_gate ON autonomy_proposal_runs(workflow_id, label, started_at);

CREATE TABLE autonomy_rule_proposals (
    id                 TEXT PRIMARY KEY,
    workflow_id        TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    label              TEXT NOT NULL,
    repo_id            TEXT REFERENCES repos(id) ON DELETE CASCADE,
    path_globs         TEXT NOT NULL DEFAULT '[]',
    task_type          TEXT,
    min_changed_lines  INTEGER,
    rule_key           TEXT NOT NULL,
    rationale          TEXT NOT NULL DEFAULT '',
    evidence           TEXT NOT NULL DEFAULT '[]',
    caught             INTEGER NOT NULL DEFAULT 0,
    evidence_total     INTEGER NOT NULL DEFAULT 0,
    cost               INTEGER NOT NULL DEFAULT 0,
    clean_approvals    INTEGER NOT NULL DEFAULT 0,
    unknown            INTEGER NOT NULL DEFAULT 0,
    status             TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed','accepted','dismissed')),
    run_id             TEXT REFERENCES autonomy_proposal_runs(id) ON DELETE SET NULL,
    provider_config_id TEXT,
    accepted_rule_id   TEXT REFERENCES autonomy_ask_rules(id) ON DELETE SET NULL,
    decided_by         TEXT NOT NULL DEFAULT '',
    decided_at         DATETIME,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX idx_autonomy_rule_proposals_key ON autonomy_rule_proposals(workflow_id, label, rule_key);
CREATE INDEX idx_autonomy_rule_proposals_status ON autonomy_rule_proposals(workflow_id, label, status);
