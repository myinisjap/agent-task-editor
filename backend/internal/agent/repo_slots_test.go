package agent

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestCountActiveRunsByRepo_OnlyPendingAndRunningConsumeSlots pins the meaning
// of the per-repo capacity count: a task whose active_agent_run_id points at a
// waiting_human (or otherwise terminal/phantom) run keeps its re-dispatch lock
// but occupies no worker, so it must not count; pending and running runs do.
func TestCountActiveRunsByRepo_OnlyPendingAndRunningConsumeSlots(t *testing.T) {
	f, err := os.CreateTemp("", "slots-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q := gen.New(db.SQL())
	ctx := context.Background()

	wfID := seedE2EWorkflow(t, q)
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "repo", Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	pc, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{ID: uuid.NewString(), Name: "p", Provider: "fake", Model: "none", Env: `{}`})
	if err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	ac, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "a", ProviderConfigID: pc.ID, Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1,
	})
	if err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	// lockedTask creates a task holding an active-run lock on a run with the
	// given status.
	lockedTask := func(status string) string {
		taskID := uuid.NewString()
		if _, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: taskID, Title: status, WorkflowID: wfID, RepoID: repoID, Label: "ready"}); err != nil {
			t.Fatalf("create task: %v", err)
		}
		run, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: taskID, AgentConfigID: &ac.ID})
		if err != nil {
			t.Fatalf("create run: %v", err)
		}
		if status != "pending" {
			if _, err := q.UpdateAgentRunStatus(ctx, gen.UpdateAgentRunStatusParams{Status: status, ID: run.ID}); err != nil {
				t.Fatalf("set run status: %v", err)
			}
		}
		if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{CurrentAgentRunID: &run.ID, ActiveAgentRunID: &run.ID, ID: taskID}); err != nil {
			t.Fatalf("lock task: %v", err)
		}
		return taskID
	}
	count := func() int64 {
		rows, err := q.CountActiveRunsByRepo(ctx)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		for _, r := range rows {
			if r.RepoID == repoID {
				return r.InUse
			}
		}
		return 0
	}

	lockedTask("waiting_human")
	lockedTask("waiting_human")
	lockedTask("failed")
	if got := count(); got != 0 {
		t.Fatalf("waiting_human/failed locks must not consume slots, got %d", got)
	}

	lockedTask("pending")
	if got := count(); got != 1 {
		t.Fatalf("a pending run should consume one slot, got %d", got)
	}
	runningTask := lockedTask("running")
	if got := count(); got != 2 {
		t.Fatalf("pending + running should consume two slots, got %d", got)
	}

	if _, err := q.SetTaskArchived(ctx, gen.SetTaskArchivedParams{Archived: 1, ID: runningTask}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if got := count(); got != 1 {
		t.Fatalf("archived task must not count, got %d", got)
	}
}
