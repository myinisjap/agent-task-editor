package storage

import "testing"

func TestMigration083AutonomyAct(t *testing.T) {
	db, err := Open(t.TempDir() + "/mig083.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	sqlDB := db.SQL()
	m := newMigrator063(t, db)
	if err := m.Migrate(82); err != nil {
		t.Fatalf("to 82: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "autonomy_decisions", "outcome")
	if err := m.Migrate(83); err != nil {
		t.Fatalf("up to 83: %v", err)
	}
	for _, c := range []string{"outcome", "hold_seconds", "due_at", "note", "outcome_at", "outcome_actor"} {
		assertColumnPresent(t, sqlDB, "autonomy_decisions", c)
	}
	assertColumnPresent(t, sqlDB, "autonomy_escapes", "source")
	if err := m.Migrate(82); err != nil {
		t.Fatalf("down to 82: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "autonomy_decisions", "outcome")
	assertColumnAbsent(t, sqlDB, "autonomy_decisions", "outcome_actor")
}
