package storage

import (
	"encoding/json"
	"testing"
)

// TestMigration075BackfillsVersionOne seeds an agent config at schema 74,
// migrates up, and checks a v1 snapshot row exists with the expected keys
// and normalised types.
func TestMigration075BackfillsVersionOne(t *testing.T) {
	db, err := Open(t.TempDir() + "/mig075.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	sqlDB := db.SQL()
	m := newMigrator063(t, db)
	if err := m.Migrate(74); err != nil {
		t.Fatalf("to 74: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "agent_runs", "agent_config_version")
	for _, stmt := range []string{
		`INSERT INTO provider_configs (id, name, provider, model, env) VALUES ('pc1', 'p', 'claude', 'sonnet', '{}')`,
		`INSERT INTO agent_configs (id, name, provider_config_id, system_prompt, labels, resume_sessions, subtasks_enabled, enabled_plugins, fallback_provider_config_ids)
		 VALUES ('ac1', 'agent', 'pc1', 'be nice', '[]', 1, 0, '["x"]', 'not json')`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := m.Migrate(75); err != nil {
		t.Fatalf("up to 74: %v", err)
	}
	assertColumnPresent(t, sqlDB, "agent_runs", "agent_config_version")

	var version int
	var raw string
	if err := sqlDB.QueryRow(`SELECT version, snapshot FROM agent_config_versions WHERE agent_config_id = 'ac1'`).Scan(&version, &raw); err != nil {
		t.Fatalf("v1 row: %v", err)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("snapshot json: %v", err)
	}
	if version != 1 || snap["system_prompt"] != "be nice" || snap["resume_sessions"] != true || snap["subtasks_enabled"] != false {
		t.Fatalf("snapshot = %v (version %d)", snap, version)
	}
	if p, ok := snap["enabled_plugins"].([]any); !ok || len(p) != 1 {
		t.Fatalf("enabled_plugins = %v", snap["enabled_plugins"])
	}
	if f, ok := snap["fallback_provider_config_ids"].([]any); !ok || len(f) != 0 {
		t.Fatalf("invalid fallbacks should normalise to []: %v", snap["fallback_provider_config_ids"])
	}
}
