package storage

import "testing"

func TestMigration080LabelDisplayName(t *testing.T) {
	db, err := Open(t.TempDir() + "/mig080.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	sqlDB := db.SQL()
	m := newMigrator063(t, db)
	if err := m.Migrate(79); err != nil {
		t.Fatalf("to 79: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "workflow_labels", "display_name")
	if err := m.Migrate(80); err != nil {
		t.Fatalf("up to 80: %v", err)
	}
	assertColumnPresent(t, sqlDB, "workflow_labels", "display_name")
	if err := m.Migrate(79); err != nil {
		t.Fatalf("down to 79: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "workflow_labels", "display_name")
}
