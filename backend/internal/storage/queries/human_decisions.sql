-- name: UpsertHumanQuestion :exec
INSERT INTO task_human_decisions (run_id, task_id, label, escalation_reason, question)
VALUES (?1, ?2, (SELECT label FROM tasks WHERE id = ?2), ?3, ?4)
ON CONFLICT(run_id) DO UPDATE SET escalation_reason = excluded.escalation_reason, question = excluded.question;

-- name: RecordHumanReply :exec
INSERT INTO task_human_decisions (run_id, task_id, label, escalation_reason, question, reply, reply_run_id, replied_at)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, CURRENT_TIMESTAMP)
ON CONFLICT(run_id) DO UPDATE SET reply = excluded.reply, reply_run_id = excluded.reply_run_id, replied_at = CURRENT_TIMESTAMP;

-- name: ListTaskHumanDecisions :many
SELECT d.run_id, d.label, d.escalation_reason, d.question, d.reply, d.replied_at, d.asked_at,
       COALESCE(ac.name, '') AS agent_name
FROM task_human_decisions d
LEFT JOIN agent_runs ar ON ar.id = d.run_id
LEFT JOIN agent_configs ac ON ac.id = ar.agent_config_id
WHERE d.task_id = ?1 AND d.reply IS NOT NULL
ORDER BY d.replied_at ASC, d.run_id ASC;
