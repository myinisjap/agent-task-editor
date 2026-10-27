ALTER TABLE repos ADD COLUMN preview_command TEXT NOT NULL DEFAULT '';
ALTER TABLE repos ADD COLUMN preview_routes TEXT NOT NULL DEFAULT '';
ALTER TABLE repos ADD COLUMN preview_paths_glob TEXT NOT NULL DEFAULT '';

CREATE TABLE task_preview_captures (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id TEXT REFERENCES agent_runs(id) ON DELETE SET NULL,
  status TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at DATETIME
);
CREATE INDEX idx_task_preview_captures_task ON task_preview_captures(task_id, created_at DESC);

CREATE TABLE task_preview_images (
  id TEXT PRIMARY KEY,
  capture_id TEXT NOT NULL REFERENCES task_preview_captures(id) ON DELETE CASCADE,
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  route TEXT NOT NULL,
  variant TEXT NOT NULL DEFAULT 'after',
  width INTEGER NOT NULL,
  height INTEGER NOT NULL,
  path TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_task_preview_images_capture ON task_preview_images(capture_id);
