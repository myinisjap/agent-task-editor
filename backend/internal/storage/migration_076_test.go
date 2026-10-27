package storage

import "testing"

func TestMigration076AgentExperiments(t *testing.T) {
	db, err := Open(t.TempDir() + "/mig076.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	sqlDB := db.SQL()
	m := newMigrator063(t, db)
	if err := m.Migrate(75); err != nil {
		t.Fatalf("to 75: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "agent_runs", "experiment_id")
	if err := m.Migrate(76); err != nil {
		t.Fatalf("up to 76: %v", err)
	}
	assertColumnPresent(t, sqlDB, "agent_runs", "experiment_id")
	assertColumnPresent(t, sqlDB, "agent_runs", "experiment_arm")
	for _, stmt := range []string{
		`INSERT INTO provider_configs (id, name, provider, model, env) VALUES ('pc1', 'p', 'claude', 'sonnet', '{}')`,
		`INSERT INTO agent_configs (id, name, provider_config_id, labels) VALUES ('ac1', 'agent', 'pc1', '[]')`,
		`INSERT INTO agent_experiments (id, agent_config_id, candidate_snapshot, share_percent, status) VALUES ('e1', 'ac1', '{}', 50, 'running')`,
		`INSERT INTO agent_experiments (id, agent_config_id, candidate_snapshot, share_percent, status) VALUES ('e2', 'ac1', '{}', 50, 'stopped')`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := sqlDB.Exec(`INSERT INTO agent_experiments (id, agent_config_id, candidate_snapshot, share_percent) VALUES ('e3', 'ac1', '{}', 50)`); err == nil {
		t.Fatal("second running experiment for the same config should be rejected")
	}
	if _, err := sqlDB.Exec(`INSERT INTO agent_experiments (id, agent_config_id, candidate_snapshot, share_percent, status) VALUES ('e4', 'ac1', '{}', 95, 'stopped')`); err == nil {
		t.Fatal("share_percent 95 should be rejected")
	}
	if err := m.Migrate(75); err != nil {
		t.Fatalf("down to 75: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "agent_runs", "experiment_id")
}
