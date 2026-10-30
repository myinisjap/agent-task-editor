UPDATE task_label_history SET actor_id = NULL
WHERE actor_id = 'pr-feedback-sync' AND note = 'GitHub PR review feedback ingested';
