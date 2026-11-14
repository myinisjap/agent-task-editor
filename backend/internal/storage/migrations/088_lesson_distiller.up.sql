-- Model-proposed repo lessons (the distiller). Proposals are ordinary
-- repo_lessons rows with status 'proposed' and source 'distiller'; they are
-- never injected until a person accepts them. lesson_distill_runs records each
-- run and its cost; lesson_dismissals remembers dismissed proposals so the same
-- lesson is not offered again.
CREATE TABLE lesson_distill_runs (
    id                 TEXT PRIMARY KEY,
    repo_id            TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    trigger            TEXT NOT NULL CHECK (trigger IN ('schedule','manual')),
    actor              TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','skipped')),
    provider_config_id TEXT,
    evidence_count     INTEGER NOT NULL DEFAULT 0,
    proposal_count     INTEGER NOT NULL DEFAULT 0,
    error              TEXT NOT NULL DEFAULT '',
    cost_usd           REAL NOT NULL DEFAULT 0,
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    started_at         DATETIME NOT NULL,
    finished_at        DATETIME
);
CREATE INDEX idx_lesson_distill_runs_repo ON lesson_distill_runs(repo_id, started_at);

CREATE TABLE lesson_dismissals (
    id           TEXT PRIMARY KEY,
    repo_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    body         TEXT NOT NULL,
    dismissed_by TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_lesson_dismissals_repo ON lesson_dismissals(repo_id);
