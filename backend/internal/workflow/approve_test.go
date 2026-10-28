package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func lastHistory(t *testing.T, q *gen.Queries, taskID string) gen.TaskLabelHistory {
	t.Helper()
	h, err := q.ListTaskLabelHistory(context.Background(), taskID)
	if err != nil || len(h) == 0 {
		t.Fatalf("history: %v %v", h, err)
	}
	return h[len(h)-1]
}

func TestEngineApprove_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})
	q := gen.New(db.SQL())
	task := createTestTask(t, db, "review", wfID)

	target, err := engine.Approve(context.Background(), task.ID, workflow.ApproveOptions{Actor: "policy", Note: "Auto-approved: x", ExpectedFrom: "review"})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := q.GetTask(context.Background(), task.ID)
	if after.Label != target || target == "review" {
		t.Fatalf("label = %s target = %s", after.Label, target)
	}
	h := lastHistory(t, q, task.ID)
	if h.Trigger != "human" || h.ActorID == nil || *h.ActorID != "policy" || h.Note == nil || *h.Note != "Auto-approved: x" {
		t.Fatalf("history = %+v", h)
	}
}

func TestEngineApprove_Errors(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})
	q := gen.New(db.SQL())
	ctx := context.Background()

	if _, err := engine.Approve(ctx, "nope", workflow.ApproveOptions{}); !errors.Is(err, workflow.ErrTaskNotFound) {
		t.Fatalf("missing task: %v", err)
	}
	work := createTestTask(t, db, "work", wfID)
	if _, err := engine.Approve(ctx, work.ID, workflow.ApproveOptions{}); !errors.Is(err, workflow.ErrNoHumanPath) {
		t.Fatalf("no path: %v", err)
	}
	task := createTestTask(t, db, "review", wfID)
	if _, err := engine.Approve(ctx, task.ID, workflow.ApproveOptions{ExpectedFrom: "work"}); !errors.Is(err, workflow.ErrStale) {
		t.Fatalf("stale: %v", err)
	}
	for _, status := range []string{"pending", "running"} {
		run, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: task.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().Exec("UPDATE agent_runs SET status = ? WHERE id = ?", status, run.ID); err != nil {
			t.Fatal(err)
		}
		if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{CurrentAgentRunID: &run.ID, ActiveAgentRunID: &run.ID, ID: task.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Approve(ctx, task.ID, workflow.ApproveOptions{}); !errors.Is(err, workflow.ErrRunLive) {
			t.Fatalf("live %s: %v", status, err)
		}
	}
}

func TestEngineApprove_EnqueuesCreatePR(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	q := gen.New(db.SQL())
	task := createTestTask(t, db, "review", wfID)
	engine := workflow.New(db.SQL(), &noopPublisher{})
	target, err := engine.Approve(context.Background(), task.ID, workflow.ApproveOptions{Actor: "u"})
	if err != nil {
		t.Fatal(err)
	}
	// Mark the destination create_pr on a fresh task and approve again.
	markCreatePR(t, db, wfID, target)
	var calls int
	engine.OnCreatePR = func(_ context.Context, _ gen.Task) error { calls++; return nil }
	task2 := createTestTask(t, db, "review", wfID)
	if _, err := engine.Approve(context.Background(), task2.ID, workflow.ApproveOptions{Actor: "policy"}); err != nil {
		t.Fatal(err)
	}
	engine.DrainSideEffects(context.Background())
	if calls != 1 {
		t.Fatalf("create_pr calls = %d", calls)
	}
	_ = q
}

func TestEngineMoveBack_NoForcedPrefix(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})
	q := gen.New(db.SQL())
	task := createTestTask(t, db, "work", wfID)
	if err := engine.MoveBack(context.Background(), task.ID, "review", "me", "Undo auto-approval", "work"); err != nil {
		t.Fatal(err)
	}
	h := lastHistory(t, q, task.ID)
	if h.Note == nil || *h.Note != "Undo auto-approval" || h.ToLabel != "review" || h.Trigger != "human" {
		t.Fatalf("history = %+v", h)
	}
}
