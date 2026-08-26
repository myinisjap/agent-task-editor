-- Optional per-repo container image to run agent CLIs in instead of
-- in-process on the backend host. Empty string (the default) means
-- "run in-process", preserving today's behavior exactly.
ALTER TABLE repos ADD COLUMN runtime_image TEXT NOT NULL DEFAULT '';
