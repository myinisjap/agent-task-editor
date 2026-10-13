package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// waitForWaitingHuman seeds a task and blocks until its first run is
// waiting_human with the task locked on it.
func (h *e2eHarness) waitForWaitingHuman(t *testing.T, taskID string) gen.Task {
	t.Helper()
	return h.pollTask(t, taskID, func(tk gen.Task) bool {
		if tk.ActiveAgentRunID == nil {
			return false
		}
		run, err := h.q.GetAgentRun(context.Background(), *tk.ActiveAgentRunID)
		return err == nil && run.Status == "waiting_human"
	}, "run to reach waiting_human")
}

func (h *e2eHarness) countRuns(t *testing.T, taskID string) int {
	t.Helper()
	runs, err := h.q.ListAgentRuns(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	return len(runs)
}

// TestE2E_ConcurrentDispatchReply_OnlyOneWins fires many simultaneous replies
// at one waiting_human task. The lock claim is a compare-and-swap, so exactly
// one reply may start a run; every other must fail with a conflict-class error
// and must not leave an orphaned run row behind.
func TestE2E_ConcurrentDispatchReply_OnlyOneWins(t *testing.T) {
	question := "which approach?"
	started := make(chan struct{})
	release := make(chan struct{})
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "waiting_human", Message: &question}},
		{started: started, release: release, result: Result{Status: "completed", Outcome: "success"}},
	}}
	h := newE2EHarness(t, fp)
	// Registered after the harness cleanup, so it runs first: unblock the run
	// before the harness waits for the pool to drain.
	t.Cleanup(func() { close(release) })
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReady(t, wfID)
	h.waitForWaitingHuman(t, taskID)

	const n = 8
	var (
		wg      sync.WaitGroup
		gate    = make(chan struct{})
		mu      sync.Mutex
		winners []string
		losers  []error
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			id, err := h.disp.DispatchReply(context.Background(), taskID, "go")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				winners = append(winners, id)
			} else {
				losers = append(losers, err)
			}
		}()
	}
	close(gate)
	wg.Wait()

	if len(winners) != 1 {
		t.Fatalf("expected exactly 1 successful reply, got %d (losers: %v)", len(winners), losers)
	}
	for _, err := range losers {
		if !errors.Is(err, ErrDispatchConflict) && !errors.Is(err, ErrRunNotWaiting) {
			t.Errorf("loser got unexpected error: %v", err)
		}
	}
	<-started
	if got := h.countRuns(t, taskID); got != 2 {
		t.Errorf("expected 2 run rows (waiting + one reply), got %d", got)
	}
	tk, err := h.q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.ActiveAgentRunID == nil || *tk.ActiveAgentRunID != winners[0] {
		t.Errorf("expected lock held by winning run %q, got %v", winners[0], tk.ActiveAgentRunID)
	}
}

// TestE2E_StaleClaim_Rejected drives startRun with stale task snapshots, the
// shape of both the reply race (lock holder changed) and the sweep race (lock
// taken / label moved after the task was listed). Each must fail with
// ErrDispatchConflict, start no run, and leave no run row behind.
func TestE2E_StaleClaim_Rejected(t *testing.T) {
	question := "need input"
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "waiting_human", Message: &question}},
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReady(t, wfID)
	locked := h.waitForWaitingHuman(t, taskID)
	cfgs, err := h.q.ListAgentConfigs(context.Background())
	if err != nil || len(cfgs) == 0 {
		t.Fatalf("list configs: %v", err)
	}
	cfg := cfgs[0]
	ctx := context.Background()

	// Sweep snapshot taken before the lock was held: expects a free lock.
	staleFree := locked
	staleFree.ActiveAgentRunID = nil
	if _, err := h.disp.startRun(ctx, staleFree, cfg, runOptions{}); !errors.Is(err, ErrDispatchConflict) {
		t.Fatalf("sweep claim against a held lock: want ErrDispatchConflict, got %v", err)
	}

	// Reply snapshot naming a run that does not hold the lock.
	other := "some-other-run"
	staleHolder := locked
	staleHolder.ActiveAgentRunID = &other
	if _, err := h.disp.startRun(ctx, staleHolder, cfg, runOptions{}); !errors.Is(err, ErrDispatchConflict) {
		t.Fatalf("reply claim naming the wrong holder: want ErrDispatchConflict, got %v", err)
	}

	// A human moves the task off the label; a sweep snapshot still carrying the
	// old label (and a free lock, as after the move) must not claim it.
	if err := h.engine.Transition(ctx, taskID, "parked", workflow.TriggerHuman, "human-1", "moved"); err != nil {
		t.Fatalf("human transition: %v", err)
	}
	staleLabel := locked
	staleLabel.ActiveAgentRunID = nil
	if _, err := h.disp.startRun(ctx, staleLabel, cfg, runOptions{}); !errors.Is(err, ErrDispatchConflict) {
		t.Fatalf("sweep claim against a moved task: want ErrDispatchConflict, got %v", err)
	}

	if got := h.countRuns(t, taskID); got != 1 {
		t.Errorf("expected only the original run row, got %d", got)
	}
	tk, err := h.q.GetTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Label != "parked" || tk.ActiveAgentRunID != nil {
		t.Errorf("task must be untouched by rejected claims: label=%q lock=%v", tk.Label, tk.ActiveAgentRunID)
	}
	fp.mu.Lock()
	invocations := len(fp.inputs)
	fp.mu.Unlock()
	if invocations != 1 {
		t.Errorf("expected provider invoked once (original run), got %d", invocations)
	}
}
