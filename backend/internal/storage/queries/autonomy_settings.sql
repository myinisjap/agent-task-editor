-- Singleton settings row for escape step-down (ADR 0031 pattern; 084_autonomy_rules).
-- Read fresh on every escape, so a change takes effect without a restart.

-- name: GetAutonomySettings :one
SELECT id, step_down_escapes, step_down_window_days, updated_at FROM autonomy_settings WHERE id = 1;

-- name: UpsertAutonomySettings :one
INSERT INTO autonomy_settings (id, step_down_escapes, step_down_window_days, updated_at)
VALUES (1, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT (id) DO UPDATE SET
    step_down_escapes = excluded.step_down_escapes,
    step_down_window_days = excluded.step_down_window_days,
    updated_at = CURRENT_TIMESTAMP
RETURNING id, step_down_escapes, step_down_window_days, updated_at;
