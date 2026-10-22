package agent

import (
	"context"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const priorFeedback = "please fix the widget"

// seedPriorFeedbackTask seeds a task with a prior run (current_agent_run_id)
// carrying priorFeedback, then moves it to "ready".
func (h *e2eHarness) seedPriorFeedbackTask(t *testing.T, wfID string) (taskID, priorRunID string) {
	t.Helper()
	taskID, cfgID := h.seedTaskWithBudgets(t, wfID, 0, 0)
	return taskID, h.seedRunWithCost(t, taskID, cfgID, 0)
}

// waitUnlockedAt waits until the task is on label and no run holds the lock.
func (h *e2eHarness) waitUnlockedAt(t *testing.T, taskID, label string) gen.Task {
	t.Helper()
	return h.pollTask(t, taskID, func(tk gen.Task) bool {
		return tk.Label == label && tk.ActiveAgentRunID == nil
	}, "task to settle on "+label)
}

func (h *e2eHarness) backToReady(t *testing.T, taskID string) {
	t.Helper()
	if _, err := h.sqlDB.Exec(`UPDATE tasks SET label='ready' WHERE id=?`, taskID); err != nil {
		t.Fatalf("move back to ready: %v", err)
	}
}

func (h *e2eHarness) currentRunFeedback(t *testing.T, taskID string) *string {
	t.Helper()
	tk, err := h.q.GetTask(context.Background(), taskID)
	if err != nil || tk.CurrentAgentRunID == nil {
		t.Fatalf("get current run: %v", err)
	}
	r, err := h.q.GetAgentRun(context.Background(), *tk.CurrentAgentRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	return r.Feedback
}

func TestFeedbackCarry_CarriedAfterFailure(t *testing.T) {
	msg := "tests still fail"
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "completed", Outcome: "failure", Message: &msg}},
		{result: Result{Status: "completed", Outcome: "failure", Message: &msg}},
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID, _ := h.seedPriorFeedbackTask(t, wfID)

	h.waitUnlockedAt(t, taskID, "final")
	if got := fp.input(t, 0).Feedback; got == nil || *got != priorFeedback {
		t.Fatalf("run 1 feedback = %v, want %q", got, priorFeedback)
	}
	if fb := h.currentRunFeedback(t, taskID); fb == nil || *fb != msg {
		t.Fatalf("row feedback = %v, want %q", fb, msg)
	}
	h.backToReady(t, taskID)
	waitInputs(t, fp, 2)
	if got := fp.input(t, 1).Feedback; got == nil || *got != msg {
		t.Fatalf("run 2 feedback = %v, want %q", got, msg)
	}
}

func TestFeedbackCarry_NotCarriedAfterSuccess(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "completed", Outcome: "success"}},
		{result: Result{Status: "completed", Outcome: "success"}},
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID, _ := h.seedPriorFeedbackTask(t, wfID)

	h.waitUnlockedAt(t, taskID, "next")
	if got := fp.input(t, 0).Feedback; got == nil || *got != priorFeedback {
		t.Fatalf("run 1 feedback = %v, want %q", got, priorFeedback)
	}
	if fb := h.currentRunFeedback(t, taskID); fb != nil {
		t.Fatalf("row feedback = %q, want nil", *fb)
	}
	h.backToReady(t, taskID)
	h.waitUnlockedAt(t, taskID, "next")
	if got := fp.input(t, 1).Feedback; got != nil && *got != "" {
		t.Fatalf("run 2 feedback = %q, want none", *got)
	}
}

func TestFeedbackCarry_CarriedAcrossTransientRetry(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{
		{err: &ErrTransient{Cause: context.DeadlineExceeded}, result: Result{Status: "failed"}},
		{result: Result{Status: "completed", Outcome: "success"}},
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID, _ := h.seedPriorFeedbackTask(t, wfID)

	h.waitUnlockedAt(t, taskID, "next")
	for i := 0; i < 2; i++ {
		if got := fp.input(t, i).Feedback; got == nil || *got != priorFeedback {
			t.Fatalf("run %d feedback = %v, want %q", i+1, got, priorFeedback)
		}
	}
	if fb := h.currentRunFeedback(t, taskID); fb != nil {
		t.Fatalf("row feedback after success = %q, want nil", *fb)
	}
}

func TestFeedbackCarry_WaitingHumanReplyKeepsFeedback(t *testing.T) {
	q := "which approach?"
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "waiting_human", Message: &q}},
		{result: Result{Status: "completed", Outcome: "success"}},
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID, _ := h.seedPriorFeedbackTask(t, wfID)

	h.pollTask(t, taskID, func(tk gen.Task) bool {
		if tk.ActiveAgentRunID == nil {
			return false
		}
		r, err := h.q.GetAgentRun(context.Background(), *tk.ActiveAgentRunID)
		return err == nil && r.Status == "waiting_human"
	}, "waiting_human")
	if _, err := h.disp.DispatchReply(context.Background(), taskID, "B"); err != nil {
		t.Fatalf("DispatchReply: %v", err)
	}
	h.waitUnlockedAt(t, taskID, "next")
	if got := fp.input(t, 1).Feedback; got == nil || *got != priorFeedback {
		t.Fatalf("reply run feedback = %v, want %q", got, priorFeedback)
	}
}

func TestFeedbackCarry_AppendedDuringRunSurvivesSuccess(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "completed", Outcome: "success"}, started: started, release: release},
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID, _ := h.seedPriorFeedbackTask(t, wfID)

	<-started
	tk, err := h.q.GetTask(context.Background(), taskID)
	if err != nil || tk.ActiveAgentRunID == nil {
		t.Fatalf("expected active run: %v", err)
	}
	g := "new review comment"
	combined := priorFeedback + "\n\n" + g
	if err := h.q.SetAgentRunFeedback(context.Background(), gen.SetAgentRunFeedbackParams{
		Feedback: &combined, ID: *tk.ActiveAgentRunID,
	}); err != nil {
		t.Fatalf("append feedback: %v", err)
	}
	close(release)

	h.waitUnlockedAt(t, taskID, "next")
	if fb := h.currentRunFeedback(t, taskID); fb == nil || *fb != g {
		t.Fatalf("row feedback = %v, want %q", fb, g)
	}
}

func waitInputs(t *testing.T, fp *fakeProvider, n int) {
	t.Helper()
	for i := 0; i < 1500; i++ {
		fp.mu.Lock()
		got := len(fp.inputs)
		fp.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d provider invocations", n)
}
