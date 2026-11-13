-- Steering notes: a human note sent to a live (running) run is stored alongside
-- replies. kind distinguishes them; actor is the named API token that sent it.
ALTER TABLE task_human_decisions ADD COLUMN kind TEXT NOT NULL DEFAULT 'reply';
ALTER TABLE task_human_decisions ADD COLUMN actor TEXT NOT NULL DEFAULT '';
