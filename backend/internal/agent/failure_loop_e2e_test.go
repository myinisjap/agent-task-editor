package agent

import (
	"context"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// These tests guard against unbounded re-dispatch: a released dispatch lock
// means the next sweep picks the task straight back up, so every path that
// releases it after a non-productive run must be bounded. Each test lets the
// dispatcher sweep many times and asserts a bounded run count and a task that
// ends locked on a waiting_human run.

const loopSweeps = 40

func (h *e2eHarness) listRuns(t *testing.T, taskID string) []gen.AgentRun {
	t.Helper()
	runs, err := h.q.ListAgentRuns(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	return runs
}

// assertEscalated checks the task is locked on a waiting_human run and stays so.
func (h *e2eHarness) assertEscalated(t *testing.T, taskID string) gen.Task {
	t.Helper()
	locked := h.pollTask(t, taskID, func(tk gen.Task) bool {
		if tk.ActiveAgentRunID == nil {
			return false
		}
		run, err := h.q.GetAgentRun(context.Background(), *tk.ActiveAgentRunID)
		return err == nil && run.Status == "waiting_human"
	}, "task locked on a waiting_human run")
	h.waitForSweeps(t, loopSweeps)
	still, err := h.q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if still.ActiveAgentRunID == nil || *still.ActiveAgentRunID != *locked.ActiveAgentRunID {
		t.Fatalf("expected task to stay locked on run %q, got %v", *locked.ActiveAgentRunID, still.ActiveAgentRunID)
	}
	if still.Label != "ready" {
		t.Errorf("expected task to stay on 'ready', got %q", still.Label)
	}
	return still
}

func TestE2E_GenuineFailure_EscalatesAfterLimit(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "failed"}}}}
	h := newE2EHarness(t, fp)
	taskID := h.seedTaskOnReady(t, seedE2EWorkflow(t, h.q))

	task := h.assertEscalated(t, taskID)
	if runs := h.listRuns(t, taskID); len(runs) != maxConsecutiveFailures {
		t.Errorf("expected exactly %d runs, got %d", maxConsecutiveFailures, len(runs))
	}
	if task.ConsecutiveFailureCount != maxConsecutiveFailures {
		t.Errorf("consecutive_failure_count = %d, want %d", task.ConsecutiveFailureCount, maxConsecutiveFailures)
	}
	if n := h.pub.count("task.needs_human"); n != 1 {
		t.Errorf("expected exactly 1 task.needs_human, got %d", n)
	}

	// A human moving the task gives it a fresh budget.
	if err := h.engine.Transition(context.Background(), taskID, "parked", workflow.TriggerHuman, "human-1", ""); err != nil {
		t.Fatalf("human transition: %v", err)
	}
	after, _ := h.q.GetTask(context.Background(), taskID)
	if after.ConsecutiveFailureCount != 0 || after.ActiveAgentRunID != nil {
		t.Errorf("expected counter reset and lock cleared after human transition, got count=%d active=%v", after.ConsecutiveFailureCount, after.ActiveAgentRunID)
	}
}

func TestE2E_GenuineFailure_SuccessResetsCounter(t *testing.T) {
	ok := Result{Status: "completed", Outcome: "success"}
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "failed"}},
		{result: Result{Status: "failed"}},
		{result: ok},
	}}
	h := newE2EHarness(t, fp)
	taskID := h.seedTaskOnReady(t, seedE2EWorkflow(t, h.q))

	task := h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.Label == "next" }, "task to advance after two failures")
	if task.ConsecutiveFailureCount != 0 {
		t.Errorf("expected counter reset by success, got %d", task.ConsecutiveFailureCount)
	}
	if runs := h.listRuns(t, taskID); len(runs) != 3 {
		t.Errorf("expected 3 runs, got %d", len(runs))
	}
}

func TestE2E_NoOutcome_EscalatesOnce(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed"}}}}
	h := newE2EHarness(t, fp)
	taskID := h.seedTaskOnReady(t, seedE2EWorkflow(t, h.q))

	h.assertEscalated(t, taskID)
	runs := h.listRuns(t, taskID)
	if len(runs) != 1 {
		t.Fatalf("expected exactly 1 run, got %d", len(runs))
	}
	if runs[0].Status != "waiting_human" {
		t.Errorf("run status = %q, want waiting_human", runs[0].Status)
	}
	if runs[0].Notes == nil || *runs[0].Notes == "" {
		t.Error("expected an explanatory note on the escalated run")
	}
	if n := h.pub.count("task.needs_human"); n != 1 {
		t.Errorf("expected exactly 1 task.needs_human, got %d", n)
	}
}

func TestE2E_TransientFailure_NoRetries_Escalates(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{
		{err: &ErrTransient{Cause: context.DeadlineExceeded}, result: Result{Status: "failed"}},
	}}
	h := newE2EHarness(t, fp)
	h.noRetries = true
	taskID := h.seedTaskOnReady(t, seedE2EWorkflow(t, h.q))

	h.assertEscalated(t, taskID)
	if runs := h.listRuns(t, taskID); len(runs) != 1 {
		t.Errorf("expected exactly 1 run, got %d", len(runs))
	} else if runs[0].Status != "waiting_human" {
		t.Errorf("run status = %q, want waiting_human", runs[0].Status)
	}
	if n := h.pub.count("task.needs_human"); n != 1 {
		t.Errorf("expected exactly 1 task.needs_human, got %d", n)
	}
}
