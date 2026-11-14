UPDATE workflow_transitions SET trigger_type = 'human'
WHERE from_label = 'done' AND to_label = 'pr-review' AND trigger_type = 'both'
  AND workflow_id IN (SELECT id FROM workflows WHERE name = 'PR review');
