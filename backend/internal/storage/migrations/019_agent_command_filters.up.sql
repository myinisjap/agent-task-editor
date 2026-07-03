ALTER TABLE agent_configs ADD COLUMN command_allowlist TEXT NOT NULL DEFAULT '[]';
ALTER TABLE agent_configs ADD COLUMN command_denylist TEXT NOT NULL DEFAULT '[]';
