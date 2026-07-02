ALTER TABLE agent_configs ADD COLUMN enabled_plugins TEXT NOT NULL DEFAULT '[]';
ALTER TABLE agent_configs ADD COLUMN enabled_mcp_servers TEXT NOT NULL DEFAULT '[]';
