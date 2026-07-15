ALTER TABLE chat_sessions ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_sessions ADD COLUMN model TEXT NOT NULL DEFAULT '';

UPDATE chat_sessions
SET provider = (SELECT provider FROM provider_configs WHERE provider_configs.id = chat_sessions.provider_config_id),
    model = (SELECT model FROM provider_configs WHERE provider_configs.id = chat_sessions.provider_config_id)
WHERE provider_config_id IS NOT NULL;

DROP INDEX IF EXISTS idx_chat_sessions_provider_config;
ALTER TABLE chat_sessions DROP COLUMN provider_config_id;

ALTER TABLE agent_configs ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_configs ADD COLUMN model TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_configs ADD COLUMN env TEXT NOT NULL DEFAULT '{}';

UPDATE agent_configs
SET provider = (SELECT provider FROM provider_configs WHERE provider_configs.id = agent_configs.provider_config_id),
    model = (SELECT model FROM provider_configs WHERE provider_configs.id = agent_configs.provider_config_id),
    env = (SELECT env FROM provider_configs WHERE provider_configs.id = agent_configs.provider_config_id)
WHERE provider_config_id IS NOT NULL;

DROP INDEX IF EXISTS idx_agent_configs_provider_config;
ALTER TABLE agent_configs DROP COLUMN provider_config_id;

DROP TABLE provider_configs;
