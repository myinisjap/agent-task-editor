DROP INDEX idx_autonomy_escapes_task;
DROP TABLE autonomy_escapes;
DROP INDEX idx_autonomy_decisions_outcome_due;
ALTER TABLE autonomy_decisions DROP COLUMN outcome_actor;
ALTER TABLE autonomy_decisions DROP COLUMN outcome_at;
ALTER TABLE autonomy_decisions DROP COLUMN note;
ALTER TABLE autonomy_decisions DROP COLUMN due_at;
ALTER TABLE autonomy_decisions DROP COLUMN hold_seconds;
ALTER TABLE autonomy_decisions DROP COLUMN outcome;
