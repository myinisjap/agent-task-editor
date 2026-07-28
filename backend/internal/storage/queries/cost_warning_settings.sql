-- Singleton settings row controlling the mid-run cost early-warning
-- threshold (see providers/cost_watchdog.go and Dispatcher.checkCostBudget;
-- 050_cost_warning). The dispatcher/watchdog read GetCostWarningSettings
-- fresh on every relevant check, so a change here takes effect without a
-- process restart.

-- name: GetCostWarningSettings :one
SELECT id, warn_ratio, updated_at FROM cost_warning_settings WHERE id = 1;

-- name: UpsertCostWarningSettings :one
INSERT INTO cost_warning_settings (id, warn_ratio, updated_at)
VALUES (1, ?, CURRENT_TIMESTAMP)
ON CONFLICT (id) DO UPDATE SET
    warn_ratio = excluded.warn_ratio,
    updated_at = CURRENT_TIMESTAMP
RETURNING id, warn_ratio, updated_at;
