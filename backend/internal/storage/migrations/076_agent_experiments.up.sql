CREATE TABLE agent_experiments (
  id TEXT PRIMARY KEY,
  agent_config_id TEXT NOT NULL REFERENCES agent_configs(id) ON DELETE CASCADE,
  candidate_snapshot TEXT NOT NULL,
  base_version INTEGER,
  share_percent INTEGER NOT NULL CHECK (share_percent BETWEEN 1 AND 90),
  status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running','stopped','promoted')),
  note TEXT,
  actor_id TEXT,
  promoted_version INTEGER,
  started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  stopped_at DATETIME
);
CREATE UNIQUE INDEX idx_agent_experiments_one_running ON agent_experiments(agent_config_id) WHERE status = 'running';
CREATE INDEX idx_agent_experiments_config ON agent_experiments(agent_config_id, started_at);
CREATE TABLE agent_experiment_assignments (
  experiment_id TEXT NOT NULL REFERENCES agent_experiments(id) ON DELETE CASCADE,
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  arm TEXT NOT NULL CHECK (arm IN ('control','candidate')),
  assigned_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (experiment_id, task_id)
);
ALTER TABLE agent_runs ADD COLUMN experiment_id TEXT;
ALTER TABLE agent_runs ADD COLUMN experiment_arm TEXT;
CREATE INDEX idx_agent_runs_experiment ON agent_runs(experiment_id) WHERE experiment_id IS NOT NULL;
