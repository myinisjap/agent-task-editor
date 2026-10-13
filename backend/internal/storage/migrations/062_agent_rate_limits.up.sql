-- Persisted copy of the in-memory agent rate-limit registry so a 429 block
-- survives a backend restart. One row per agent config that is currently
-- blocked. blocked_until is the unblock time (UTC); attempts is the
-- consecutive 429 count that drives the escalating backoff ladder. There is
-- deliberately no foreign key: rows are cheap, expire on their own and are
-- pruned at startup.
CREATE TABLE agent_rate_limits (
    agent_config_id TEXT PRIMARY KEY,
    blocked_until   DATETIME NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 1,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
