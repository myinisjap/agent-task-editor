package storage

import "testing"

func TestMigration084AutonomyRules(t *testing.T) {
	db, err := Open(t.TempDir() + "/mig084.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	sqlDB := db.SQL()
	m := newMigrator063(t, db)
	if err := m.Migrate(83); err != nil {
		t.Fatalf("to 83: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "autonomy_escapes", "decision_id")
	if err := m.Migrate(84); err != nil {
		t.Fatalf("up to 84: %v", err)
	}
	for _, c := range []string{"decision_id", "actor", "note", "rule_id", "reset_by_change_id"} {
		assertColumnPresent(t, sqlDB, "autonomy_escapes", c)
	}
	assertColumnPresent(t, sqlDB, "autonomy_ask_rules", "path_globs")
	assertColumnPresent(t, sqlDB, "autonomy_changes", "reason")
	var n, w int
	if err := sqlDB.QueryRow(`SELECT step_down_escapes, step_down_window_days FROM autonomy_settings WHERE id = 1`).Scan(&n, &w); err != nil {
		t.Fatalf("settings row: %v", err)
	}
	if n != 2 || w != 14 {
		t.Fatalf("defaults = %d/%d, want 2/14", n, w)
	}
	if err := m.Migrate(83); err != nil {
		t.Fatalf("down to 83: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "autonomy_escapes", "decision_id")
	assertColumnAbsent(t, sqlDB, "autonomy_escapes", "reset_by_change_id")
}
