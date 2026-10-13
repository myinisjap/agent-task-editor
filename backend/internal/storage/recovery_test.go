package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
)

func TestRecoverInterruptedRuns(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := storage.SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatal(err)
	}
	sq := db.SQL()
	var wf string
	if err := sq.QueryRow(`SELECT id FROM workflows LIMIT 1`).Scan(&wf); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := sq.Exec(q, a...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO repos (id,name,path,workflow_id) VALUES ('repo1','repo','/tmp/repo1',?)`, wf)
	exec(`INSERT INTO tasks (id,title,description,type,label,repo_id,workflow_id,consecutive_failure_count,transient_retry_count,active_agent_run_id) VALUES ('t1','t','','feature','work','repo1',?,2,1,'r1')`, wf)
	exec(`INSERT INTO agent_runs (id,task_id,status) VALUES ('r1','t1','running'),('r2','t1','pending'),('r3','t1','completed'),('r4','t1','failed')`)

	n, err := storage.RecoverInterruptedRuns(ctx, db)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v, want 2", n, err)
	}
	want := map[string]string{"r1": "interrupted", "r2": "interrupted", "r3": "completed", "r4": "failed"}
	for id, st := range want {
		var got string
		if err := sq.QueryRow(`SELECT status FROM agent_runs WHERE id=?`, id).Scan(&got); err != nil || got != st {
			t.Errorf("%s status=%q err=%v want %q", id, got, err, st)
		}
	}
	var active *string
	var fails, trans int
	if err := sq.QueryRow(`SELECT active_agent_run_id, consecutive_failure_count, transient_retry_count FROM tasks WHERE id='t1'`).Scan(&active, &fails, &trans); err != nil {
		t.Fatal(err)
	}
	if active != nil || fails != 2 || trans != 1 {
		t.Errorf("active=%v fails=%d trans=%d; want nil,2,1", active, fails, trans)
	}
}
