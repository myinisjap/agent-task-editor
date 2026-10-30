-- Singleton settings row for escape step-down (ADR 0031 pattern; 084_autonomy_rules).
-- Read fresh on every escape, so a change takes effect without a restart.

-- name: GetAutonomySettings :one
SELECT id, step_down_escapes, step_down_window_days, updated_at, proposal_provider_config_id, proposal_interval_hours FROM autonomy_settings WHERE id = 1;

-- name: UpsertAutonomySettings :one
INSERT INTO autonomy_settings (id, step_down_escapes, step_down_window_days, updated_at)
VALUES (1, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT (id) DO UPDATE SET
    step_down_escapes = excluded.step_down_escapes,
    step_down_window_days = excluded.step_down_window_days,
    updated_at = CURRENT_TIMESTAMP
RETURNING id, step_down_escapes, step_down_window_days, updated_at, proposal_provider_config_id, proposal_interval_hours;

-- name: SetAutonomyProposalSettings :one
-- ADR 0040: the provider config the rule-proposal job uses (NULL = off) and
-- how often it runs per gate (0 = only on demand).
UPDATE autonomy_settings
SET proposal_provider_config_id = ?1, proposal_interval_hours = ?2, updated_at = CURRENT_TIMESTAMP
WHERE id = 1
RETURNING id, step_down_escapes, step_down_window_days, updated_at, proposal_provider_config_id, proposal_interval_hours;
