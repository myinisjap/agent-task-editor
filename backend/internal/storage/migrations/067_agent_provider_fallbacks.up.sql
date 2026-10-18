-- Ordered fallback providers per agent config. provider_config_id stays the
-- primary (first in the list); this JSON array of provider_config ids is
-- tried after it, in order, when an earlier one's account is rate-limited
-- or its provider is paused.
ALTER TABLE agent_configs ADD COLUMN fallback_provider_config_ids TEXT NOT NULL DEFAULT '[]';

-- Which provider config a run actually used (NULL for runs from before
-- fallbacks existed, which always used the agent config's primary). Keys
-- session resume and the run history's provider column.
ALTER TABLE agent_runs ADD COLUMN provider_config_id TEXT;
