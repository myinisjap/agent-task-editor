-- Optional per-repo language list for a generated devcontainer.json runtime
-- (see internal/agent/devcontainer.go). JSON array of {"id","version"}
-- objects, validated against a fixed allowlist by ParseRuntimeLanguages —
-- never a free-form user-authored blob. Empty string (the default) means
-- "no languages configured", preserving today's behavior exactly.
ALTER TABLE repos ADD COLUMN runtime_languages TEXT NOT NULL DEFAULT '';
