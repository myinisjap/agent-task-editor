-- Repo map (docs/repo-map.md): a generated, refreshed codebase guide per repo.
-- Per-repo opt-in config (off by default). A missing row means disabled.
CREATE TABLE repo_map_config (
    repo_id               TEXT PRIMARY KEY REFERENCES repos(id) ON DELETE CASCADE,
    enabled               INTEGER NOT NULL DEFAULT 0,
    provider_config_id    TEXT,
    refresh_interval_hours INTEGER NOT NULL DEFAULT 168,
    min_commits_behind    INTEGER NOT NULL DEFAULT 20,
    updated_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Global singleton: which workflow labels get the REPO MAP prompt section
-- (comma separated; default planning only) and which agent configs opted out
-- (JSON array of agent config ids).
CREATE TABLE repo_map_global (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    inject_labels      TEXT NOT NULL DEFAULT 'plan',
    excluded_agent_ids TEXT NOT NULL DEFAULT '[]',
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO repo_map_global (id) VALUES (1);

-- One row per generation attempt, with its cost.
CREATE TABLE repo_map_runs (
    id                 TEXT PRIMARY KEY,
    repo_id            TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    trigger            TEXT NOT NULL CHECK (trigger IN ('schedule','manual')),
    actor              TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','skipped')),
    commit_sha         TEXT NOT NULL DEFAULT '',
    provider_config_id TEXT,
    page_count         INTEGER NOT NULL DEFAULT 0,
    char_count         INTEGER NOT NULL DEFAULT 0,
    error              TEXT NOT NULL DEFAULT '',
    cost_usd           REAL NOT NULL DEFAULT 0,
    input_tokens       INTEGER NOT NULL DEFAULT 0,
    output_tokens      INTEGER NOT NULL DEFAULT 0,
    started_at         DATETIME NOT NULL,
    finished_at        DATETIME
);
CREATE INDEX idx_repo_map_runs_repo ON repo_map_runs(repo_id, started_at);

-- Pages of each successful run; only the last few runs' pages are kept.
CREATE TABLE repo_map_pages (
    id         TEXT PRIMARY KEY,
    repo_id    TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    run_id     TEXT NOT NULL REFERENCES repo_map_runs(id) ON DELETE CASCADE,
    slug       TEXT NOT NULL,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_repo_map_pages_run ON repo_map_pages(run_id, position);
CREATE INDEX idx_repo_map_pages_repo ON repo_map_pages(repo_id);
