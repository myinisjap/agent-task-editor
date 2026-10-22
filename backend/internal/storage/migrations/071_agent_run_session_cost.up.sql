-- The provider's own cumulative cost for the run's session, as reported at the
-- end of the run (claude's total_cost_usd counts every earlier run resumed
-- into the same session). Kept so a resumed run can record only its own share
-- in cost_usd. Rows written before this column existed recorded that same
-- cumulative figure in cost_usd, so backfill from it.
ALTER TABLE agent_runs ADD COLUMN session_cost_usd REAL;
UPDATE agent_runs SET session_cost_usd = cost_usd WHERE session_id != '' AND cost_usd > 0;
