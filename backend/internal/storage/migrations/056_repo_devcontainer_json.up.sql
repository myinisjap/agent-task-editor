-- Optional UI-authored devcontainer.json for a repo, used to build a runtime
-- container when the repo has no runtime_image set and no
-- .devcontainer/devcontainer.json committed in the repo itself (see
-- internal/agent/devcontainer.go's resolution order). Empty string (the
-- default) means "no DB-stored devcontainer config", preserving today's
-- behavior exactly.
ALTER TABLE repos ADD COLUMN devcontainer_json TEXT NOT NULL DEFAULT '';
