DROP INDEX IF EXISTS idx_agent_runs_experiment;
ALTER TABLE agent_runs DROP COLUMN experiment_arm;
ALTER TABLE agent_runs DROP COLUMN experiment_id;
DROP TABLE IF EXISTS agent_experiment_assignments;
DROP INDEX IF EXISTS idx_agent_experiments_config;
DROP INDEX IF EXISTS idx_agent_experiments_one_running;
DROP TABLE IF EXISTS agent_experiments;
