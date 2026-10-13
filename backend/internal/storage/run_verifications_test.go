package storage

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestRunVerifications_UpsertListAndCascade(t *testing.T) {
	db, err := Open(t.TempDir() + "/v.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	q := gen.New(db.SQL())
	if err := SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatal(err)
	}
	wfs, _ := q.ListWorkflows(ctx)
	repoID, taskID := uuid.NewString(), uuid.NewString()
	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfs[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if repo.VerifyCommand != "" {
		t.Errorf("default verify_command = %q", repo.VerifyCommand)
	}
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: taskID, Title: "t", WorkflowID: wfs[0].ID, RepoID: repoID, Label: "work"}); err != nil {
		t.Fatal(err)
	}
	run1, run2 := uuid.NewString(), uuid.NewString()
	for _, id := range []string{run1, run2} {
		if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: id, TaskID: taskID}); err != nil {
			t.Fatal(err)
		}
	}

	code := int64(2)
	put := func(run, status, out string) {
		t.Helper()
		if err := q.UpsertRunVerification(ctx, gen.UpsertRunVerificationParams{
			RunID: run, TaskID: taskID, Command: "c", Status: status, ExitCode: &code, Output: out,
		}); err != nil {
			t.Fatal(err)
		}
	}
	put(run1, "fail", "first")
	put(run1, "pass", "second") // upsert replaces
	put(run2, "fail", "other")

	got, err := q.GetRunVerification(ctx, run1)
	if err != nil || got.Status != "pass" || got.Output != "second" {
		t.Fatalf("upsert: %+v err=%v", got, err)
	}
	list, err := q.ListTaskVerifications(ctx, gen.ListTaskVerificationsParams{TaskID: taskID, Limit: 10})
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %d err=%v", len(list), err)
	}

	if _, err := db.SQL().Exec("DELETE FROM agent_runs WHERE id = ?", run1); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetRunVerification(ctx, run1); err == nil {
		t.Error("verification should be deleted with its run")
	}
	if _, err := db.SQL().Exec("DELETE FROM tasks WHERE id = ?", taskID); err != nil {
		t.Fatal(err)
	}
	list, _ = q.ListTaskVerifications(ctx, gen.ListTaskVerificationsParams{TaskID: taskID, Limit: 10})
	if len(list) != 0 {
		t.Errorf("verifications should be deleted with their task, %d left", len(list))
	}
}
