package agent

import (
	"log/slog"
	"strings"
	"time"

	"context"
	"github.com/google/uuid"
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
	assertEscalationReason(t, h.q, *task.ActiveAgentRunID, ReasonRepeatedFailure)
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
	assertEscalationReason(t, h.q, runs[0].ID, ReasonNoOutcome)
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
	} else {
		assertEscalationReason(t, h.q, runs[0].ID, ReasonTransientExhausted)
	}
	if n := h.pub.count("task.needs_human"); n != 1 {
		t.Errorf("expected exactly 1 task.needs_human, got %d", n)
	}
}

// seedRejectingWorkflow is seedE2EWorkflow with the success target ("next")
// marked agent_ignore, so every agent-requested ready → next transition is
// rejected by the engine (ErrAgentIgnored) while the task stays on "ready".
func seedRejectingWorkflow(t *testing.T, q *gen.Queries) string {
	t.Helper()
	ctx := context.Background()
	wfID := uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "Reject", Description: "rejected transition test"}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	for i, name := range []string{"ready", "next", "parked"} {
		ignore := int64(0)
		if name == "next" {
			ignore = 1
		}
		if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
			ID: uuid.NewString(), WorkflowID: wfID, Name: name, Color: "#000000", SortOrder: int64(i), AgentIgnore: ignore,
		}); err != nil {
			t.Fatalf("create label %s: %v", name, err)
		}
	}
	success := "success"
	for _, tr := range []struct{ from, to, trigger string }{
		{"ready", "next", "agent"},
		{"ready", "parked", "human"},
	} {
		var path *string
		if tr.to == "next" {
			path = &success
		}
		if _, err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
			ID: uuid.NewString(), WorkflowID: wfID, FromLabel: tr.from, ToLabel: tr.to, TriggerType: tr.trigger, Path: path,
		}); err != nil {
			t.Fatalf("create transition: %v", err)
		}
	}
	return wfID
}

func TestE2E_RejectedTransition_EscalatesAfterLimit(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	taskID := h.seedTaskOnReady(t, seedRejectingWorkflow(t, h.q))

	task := h.assertEscalated(t, taskID)
	runs := h.listRuns(t, taskID)
	if len(runs) != maxConsecutiveFailures {
		t.Errorf("expected exactly %d runs, got %d", maxConsecutiveFailures, len(runs))
	}
	if task.ConsecutiveFailureCount != maxConsecutiveFailures {
		t.Errorf("consecutive_failure_count = %d, want %d", task.ConsecutiveFailureCount, maxConsecutiveFailures)
	}
	assertEscalationReason(t, h.q, *task.ActiveAgentRunID, ReasonRejectedTransition)
	run, err := h.q.GetAgentRun(context.Background(), *task.ActiveAgentRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Notes == nil || !strings.Contains(*run.Notes, `"next"`) || !strings.Contains(*run.Notes, workflow.ErrAgentIgnored.Error()) {
		t.Errorf("expected note naming target label and error, got %v", run.Notes)
	}
	if n := h.pub.count("task.needs_human"); n != 1 {
		t.Errorf("expected exactly 1 task.needs_human, got %d", n)
	}
}

func TestE2E_RejectedTransition_BelowLimitReleasesLock(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "completed", Outcome: "success"}},
		{started: started, release: release},
	}}
	h := newE2EHarness(t, fp)
	// Registered after the harness so it runs first (LIFO) and unblocks the
	// parked run before the harness waits for in-flight runs to drain.
	t.Cleanup(func() { close(release) })
	taskID := h.seedTaskOnReady(t, seedRejectingWorkflow(t, h.q))

	// The second run only starts if the first rejection released the lock.
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("task was not re-dispatched after a single rejected transition")
	}
	task, _ := h.q.GetTask(context.Background(), taskID)
	if task.ConsecutiveFailureCount != 1 {
		t.Errorf("consecutive_failure_count = %d, want 1", task.ConsecutiveFailureCount)
	}
	if n := h.pub.count("task.needs_human"); n != 0 {
		t.Errorf("expected no task.needs_human below the limit, got %d", n)
	}
}

// A concurrent move (stale label, or ErrStale) is not a rejection: it must
// neither count toward the limit nor escalate.
func TestE2E_RejectedTransition_ConcurrentMoveNotCounted(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	fp := &fakeProvider{steps: []fakeStep{{started: started, release: release}}}
	h := newE2EHarness(t, fp)
	t.Cleanup(func() { close(release) })
	taskID := h.seedTaskOnReady(t, seedRejectingWorkflow(t, h.q))
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not start")
	}

	log := slog.Default()
	for _, tc := range []struct {
		name  string
		label string
		err   error
	}{
		{"stale label", "elsewhere", workflow.ErrNoTransition},
		{"ErrStale", "ready", workflow.ErrStale},
	} {
		job := Job{RunID: "other-run", Input: RunInput{Task: Task{ID: taskID, Label: tc.label}}}
		for i := 0; i < maxConsecutiveFailures+1; i++ {
			h.pool.handleRejectedTransition(context.Background(), job, Result{}, "next", tc.err, log)
		}
		task, _ := h.q.GetTask(context.Background(), taskID)
		if task.ConsecutiveFailureCount != 0 {
			t.Errorf("%s: counter = %d, want 0", tc.name, task.ConsecutiveFailureCount)
		}
	}
	if n := h.pub.count("task.needs_human"); n != 0 {
		t.Errorf("expected no task.needs_human, got %d", n)
	}
}
