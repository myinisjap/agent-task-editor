package ghsync

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// newMergedTestWorkflow seeds "work" plus a terminal "done" label, with no
// transition between them, like a board whose only route to done is review.
func newMergedTestWorkflow(t *testing.T, q *gen.Queries) string {
	t.Helper()
	ctx := context.Background()
	wfID := uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "Merged", Description: ""}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	for i, l := range []struct {
		name     string
		terminal int64
	}{{"work", 0}, {"done", 1}} {
		if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
			ID: uuid.NewString(), WorkflowID: wfID, Name: l.name, Color: "#000", SortOrder: int64(i), IsTerminal: l.terminal,
		}); err != nil {
			t.Fatalf("create label %s: %v", l.name, err)
		}
	}
	return wfID
}

// setActiveRun gives task an active run in the given status.
func setActiveRun(t *testing.T, q *gen.Queries, taskID, status string) string {
	t.Helper()
	ctx := context.Background()
	run, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: taskID})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	switch status {
	case "running":
		if _, err := q.SetAgentRunStarted(ctx, run.ID); err != nil {
			t.Fatalf("start run: %v", err)
		}
	default:
		if _, err := q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{Status: status, ID: run.ID}); err != nil {
			t.Fatalf("complete run: %v", err)
		}
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{CurrentAgentRunID: &run.ID, ActiveAgentRunID: &run.ID, ID: taskID}); err != nil {
		t.Fatalf("set active run: %v", err)
	}
	return run.ID
}

func mergedSyncer(t *testing.T) (*Syncer, *gen.Queries, string, string) {
	t.Helper()
	getPRHead := func(ctx context.Context, repo repoInfo, branch string) (forge.PRHead, error) {
		return fakePRHead("pr_merged", "https://github.com/acme/widgets/pull/1", 1), nil
	}
	s, q, _ := newTestSyncerFull(t, getPRHead, nil, nil, nil, true)
	wfID := newMergedTestWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	return s, q, wfID, repoID
}

func TestSyncTask_MergedMovesWaitingTaskToTerminalLabel(t *testing.T) {
	ctx := context.Background()
	s, q, wfID, repoID := mergedSyncer(t)
	task := newTestTask(t, q, repoID, wfID, "work", "", "", "pr_open", "")
	setActiveRun(t, q, task.ID, "waiting_human")
	task, _ = q.GetTask(ctx, task.ID)

	s.syncTask(ctx, task, repoInfo{ghName: "acme/widgets"})

	got, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "done" {
		t.Fatalf("label = %q, want done", got.Label)
	}
	if got.ActiveAgentRunID != nil {
		t.Errorf("active_agent_run_id = %v, want cleared", *got.ActiveAgentRunID)
	}
	hist, err := q.ListTaskLabelHistory(ctx, task.ID)
	if err != nil || len(hist) == 0 {
		t.Fatalf("label history: %v, %d rows", err, len(hist))
	}
	last := hist[len(hist)-1]
	if last.Note == nil || *last.Note != workflow.ForcedNotePrefix+": "+mergedNote {
		t.Errorf("history note = %v, want the PR-merged note", last.Note)
	}
}

func TestSyncTask_MergedLeavesTaskWithLiveRun(t *testing.T) {
	ctx := context.Background()
	s, q, wfID, repoID := mergedSyncer(t)
	task := newTestTask(t, q, repoID, wfID, "work", "", "", "pr_open", "")
	runID := setActiveRun(t, q, task.ID, "running")
	task, _ = q.GetTask(ctx, task.ID)

	s.syncTask(ctx, task, repoInfo{ghName: "acme/widgets"})

	got, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "work" {
		t.Errorf("label = %q, want work (live run must not be moved under)", got.Label)
	}
	if got.ActiveAgentRunID == nil || *got.ActiveAgentRunID != runID {
		t.Errorf("active_agent_run_id = %v, want %s kept", got.ActiveAgentRunID, runID)
	}
}

func TestSyncTask_MergedWithoutEngineLeavesLabel(t *testing.T) {
	ctx := context.Background()
	getPRHead := func(ctx context.Context, repo repoInfo, branch string) (forge.PRHead, error) {
		return fakePRHead("pr_merged", "https://github.com/acme/widgets/pull/1", 1), nil
	}
	s, q, _ := newTestSyncerFull(t, getPRHead, nil, nil, nil, false)
	wfID := newMergedTestWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	task := newTestTask(t, q, repoID, wfID, "work", "", "", "pr_open", "")

	s.syncTask(ctx, task, repoInfo{ghName: "acme/widgets"})

	if got, _ := q.GetTask(ctx, task.ID); got.Label != "work" {
		t.Errorf("label = %q, want work", got.Label)
	}
}
