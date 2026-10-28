-- Autonomy dial 4: the policy acts on approve decisions. State lives on
-- autonomy_decisions: an 'ask' decision has outcome NULL; an 'approve'
-- decision moves pending -> approved | held | superseded | skipped | failed,
-- and approved may later become undone.
ALTER TABLE autonomy_decisions ADD COLUMN outcome TEXT CHECK (outcome IN ('pending','approved','held','superseded','skipped','failed','undone'));
ALTER TABLE autonomy_decisions ADD COLUMN hold_seconds INTEGER;
ALTER TABLE autonomy_decisions ADD COLUMN due_at DATETIME;
ALTER TABLE autonomy_decisions ADD COLUMN note TEXT;
ALTER TABLE autonomy_decisions ADD COLUMN outcome_at DATETIME;
ALTER TABLE autonomy_decisions ADD COLUMN outcome_actor TEXT;
CREATE INDEX idx_autonomy_decisions_outcome_due ON autonomy_decisions(outcome, due_at);

CREATE TABLE autonomy_escapes (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    workflow_id TEXT NOT NULL,
    label       TEXT NOT NULL,
    source      TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_autonomy_escapes_task ON autonomy_escapes(task_id, created_at);
