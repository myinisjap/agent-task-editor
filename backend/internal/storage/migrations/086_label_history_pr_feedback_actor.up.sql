UPDATE task_label_history
SET actor_id = 'pr-feedback-sync'
WHERE trigger = 'human'
  AND note = 'GitHub PR review feedback ingested'
  AND (actor_id IS NULL OR actor_id = '');
