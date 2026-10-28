package storage

import "testing"

func TestMigration082Autonomy(t *testing.T) {
	db, err := Open(t.TempDir() + "/mig082.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	sqlDB := db.SQL()
	m := newMigrator063(t, db)
	if err := m.Migrate(81); err != nil {
		t.Fatalf("to 81: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "workflow_labels", "autonomy_mode")
	assertColumnAbsent(t, sqlDB, "tasks", "always_ask")
	if _, err := sqlDB.Exec(`INSERT INTO workflows (id, name) VALUES ('wf82', 'wf82')`); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO workflow_labels (id, workflow_id, name) VALUES ('l82', 'wf82', 'review')`); err != nil {
		t.Fatalf("insert label: %v", err)
	}
	if err := m.Migrate(82); err != nil {
		t.Fatalf("up to 82: %v", err)
	}
	for _, c := range []string{"autonomy_mode", "autonomy_ask_on", "autonomy_hold_seconds", "autonomy_max_cost_usd"} {
		assertColumnPresent(t, sqlDB, "workflow_labels", c)
	}
	assertColumnPresent(t, sqlDB, "tasks", "always_ask")
	assertColumnPresent(t, sqlDB, "tasks_full", "always_ask")
	assertColumnPresent(t, sqlDB, "autonomy_decisions", "reasons")
	var mode string
	var hold int
	if err := sqlDB.QueryRow(`SELECT autonomy_mode, autonomy_hold_seconds FROM workflow_labels WHERE id='l82'`).Scan(&mode, &hold); err != nil {
		t.Fatalf("read label: %v", err)
	}
	if mode != "ask" || hold != 600 {
		t.Fatalf("defaults = %q/%d", mode, hold)
	}
	if err := m.Migrate(81); err != nil {
		t.Fatalf("down to 81: %v", err)
	}
	assertColumnAbsent(t, sqlDB, "workflow_labels", "autonomy_mode")
	assertColumnAbsent(t, sqlDB, "tasks", "always_ask")
	assertColumnAbsent(t, sqlDB, "tasks_full", "always_ask")
}
