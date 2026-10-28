package agent

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestSessionCostBaseline: a resumed run's baseline is the session total the
// session's latest earlier run recorded; runs that recorded none are skipped,
// and other sessions or a cold start give 0.
func TestSessionCostBaseline(t *testing.T) {
	ctx := context.Background()
	f, err := os.CreateTemp("", "session-cost-*.db")
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

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: uuid.NewString(), Name: "wf"})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "repo", Path: t.TempDir(), WorkflowID: &wf.ID}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	taskID := uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: taskID, Title: "t", WorkflowID: wf.ID, RepoID: repoID, Label: "ready"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	addRun := func(session string, sessionCost float64) {
		id := uuid.NewString()
		if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: id, TaskID: taskID}); err != nil {
			t.Fatalf("create run: %v", err)
		}
		if err := q.SetAgentRunSession(ctx, gen.SetAgentRunSessionParams{SessionID: session, ID: id}); err != nil {
			t.Fatalf("set run session: %v", err)
		}
		if sessionCost > 0 {
			if err := q.SetAgentRunSessionCost(ctx, gen.SetAgentRunSessionCostParams{SessionCostUsd: &sessionCost, ID: id}); err != nil {
				t.Fatalf("set run session cost: %v", err)
			}
		}
	}
	addRun("s1", 0.52)
	addRun("s1", 0) // e.g. killed by the cost watchdog before reporting a total
	addRun("s2", 0.10)

	d := &Dispatcher{q: q}
	for session, want := range map[string]float64{"s1": 0.52, "s2": 0.10, "s3": 0, "": 0} {
		if got := d.sessionCostBaseline(ctx, taskID, session); got != want {
			t.Errorf("baseline(%q) = %v, want %v", session, got, want)
		}
	}
}
