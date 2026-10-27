-- Escalation ladder: JSON array of rungs {provider_config_id, effort?,
-- fallback_provider_config_ids?}. Rung 0 is implicit (the agent's own
-- provider/effort); entry i is rung i+1. Empty = never escalate.
ALTER TABLE agent_configs ADD COLUMN escalation_ladder TEXT NOT NULL DEFAULT '[]';

-- Ladder rung a run used (NULL when the agent config had no ladder).
ALTER TABLE agent_runs ADD COLUMN ladder_rung INTEGER;
