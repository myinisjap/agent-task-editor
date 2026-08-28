-- Per-repo agent toolchain pins (mise-managed language versions). Empty
-- string (the default) means "unconfigured" — the dispatcher and providers
-- must take the exact same code path as before this column existed. JSON
-- array of {"id":"go","version":"1.21"} objects; validated by
-- internal/agent/runtime.ParsePins on write.
ALTER TABLE repos ADD COLUMN runtime_languages TEXT NOT NULL DEFAULT '';
