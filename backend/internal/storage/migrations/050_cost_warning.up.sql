-- Singleton settings row controlling the mid-run cost early-warning
-- threshold (see providers/cost_watchdog.go and Dispatcher.checkCostBudget).
-- id is always 1 -- a CHECK constraint enforces this is a single-row table,
-- upserted via UpsertCostWarningSettings rather than inserted freely.
--
-- warn_ratio is the fraction (0..1) of a task's effective cost budget
-- (max_cost_usd) at which the watchdog/dispatcher surface a task.cost_warning
-- event, ahead of the hard kill/exhaustion at 1.0. Defaults to 0.8 (80%),
-- matching the default described in docs/agents.md.
CREATE TABLE cost_warning_settings (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    warn_ratio REAL NOT NULL DEFAULT 0.8,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO cost_warning_settings (id, warn_ratio) VALUES (1, 0.8);

-- One-shot guard so the dispatcher's pre-dispatch warning (a task that is
-- already >= warn_ratio*budget spent when a new run is about to start, e.g.
-- on a provider that doesn't support the mid-run watchdog) fires once per
-- task rather than every sweep. Reset to 0 whenever the task's own
-- max_cost_usd changes (see SetTaskMaxCostUSD) so a raised budget can warn
-- again if spend later approaches the new ceiling.
ALTER TABLE tasks ADD COLUMN cost_warned INTEGER NOT NULL DEFAULT 0;
