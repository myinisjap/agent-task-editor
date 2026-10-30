package storage

import (
	"testing"
)

func TestMigration086PRFeedbackActorBackfill(t *testing.T) {
	db, err := Open(t.TempDir() + "/mig086.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	sqlDB := db.SQL()
	m := newMigrator063(t, db)
	if err := m.Migrate(85); err != nil {
		t.Fatalf("to 85: %v", err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := sqlDB.Exec(q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	mustExec(`INSERT INTO workflows (id, name) VALUES ('wf', 'wf')`)
	mustExec(`INSERT INTO repos (id, name, path, workflow_id) VALUES ('r', 'r', '/tmp/r', 'wf')`)
	mustExec(`INSERT INTO tasks (id, title, workflow_id, repo_id, label) VALUES ('t', 't', 'wf', 'r', 'work')`)
	const ingest = "GitHub PR review feedback ingested"
	ins := `INSERT INTO task_label_history (id, task_id, from_label, to_label, trigger, actor_id, note) VALUES (?, 't', 'review', 'work', ?, ?, ?)`
	mustExec(ins, "a", "human", nil, ingest)
	mustExec(ins, "b", "human", "alice", ingest)
	mustExec(ins, "c", "human", nil, "other")
	mustExec(ins, "d", "agent", nil, ingest)

	actor := func(id string) *string {
		t.Helper()
		var a *string
		if err := sqlDB.QueryRow(`SELECT actor_id FROM task_label_history WHERE id = ?`, id).Scan(&a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	if err := m.Migrate(86); err != nil {
		t.Fatalf("up to 86: %v", err)
	}
	if a := actor("a"); a == nil || *a != "pr-feedback-sync" {
		t.Fatalf("a = %v, want pr-feedback-sync", a)
	}
	if a := actor("b"); a == nil || *a != "alice" {
		t.Fatalf("b = %v, want alice", a)
	}
	if a := actor("c"); a != nil {
		t.Fatalf("c = %v, want nil", *a)
	}
	if a := actor("d"); a != nil {
		t.Fatalf("d = %v, want nil", *a)
	}
	if err := m.Migrate(85); err != nil {
		t.Fatalf("down to 85: %v", err)
	}
	if a := actor("a"); a != nil {
		t.Fatalf("a after down = %v, want nil", *a)
	}
}
