package storage

import "testing"

func TestMigration085AutonomyRuleProposals(t *testing.T) {
	db, err := Open(t.TempDir() + "/mig085.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	sqlDB := db.SQL()
	m := newMigrator063(t, db)
	if err := m.Migrate(84); err != nil {
		t.Fatalf("to 84: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "autonomy_settings", "proposal_provider_config_id")
	if err := m.Migrate(85); err != nil {
		t.Fatalf("up to 85: %v", err)
	}
	assertColumnPresent(t, sqlDB, "autonomy_rule_proposals", "rule_key")
	assertColumnPresent(t, sqlDB, "autonomy_proposal_runs", "flag_ideas")
	var pc *string
	var hours int
	if err := sqlDB.QueryRow(`SELECT proposal_provider_config_id, proposal_interval_hours FROM autonomy_settings WHERE id = 1`).Scan(&pc, &hours); err != nil {
		t.Fatalf("settings row: %v", err)
	}
	if pc != nil || hours != 24 {
		t.Fatalf("defaults = %v/%d, want nil/24", pc, hours)
	}
	if err := m.Migrate(84); err != nil {
		t.Fatalf("down to 84: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "autonomy_settings", "proposal_interval_hours")
}
