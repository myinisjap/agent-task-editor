DROP INDEX IF EXISTS idx_task_preview_images_capture;
DROP TABLE task_preview_images;
DROP INDEX IF EXISTS idx_task_preview_captures_task;
DROP TABLE task_preview_captures;
ALTER TABLE repos DROP COLUMN preview_paths_glob;
ALTER TABLE repos DROP COLUMN preview_routes;
ALTER TABLE repos DROP COLUMN preview_command;
