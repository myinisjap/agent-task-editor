package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// steerProvider blocks its first invocation until the run is cancelled
// (reporting a session id), then completes every later invocation.
type steerProvider struct {
	mu     sync.Mutex
	inputs []RunInput
	n      int
	first  chan struct{}
}

func (p *steerProvider) Run(ctx context.Context, in RunInput, _ chan<- LogEntry) (Result, error) {
	p.mu.Lock()
	p.n++
	n := p.n
	p.inputs = append(p.inputs, in)
	p.mu.Unlock()
	if n == 1 {
		close(p.first)
		<-ctx.Done()
		return Result{SessionID: "sess-steer"}, ctx.Err()
	}
	return Result{Status: "completed", Outcome: "success"}, nil
}

func TestE2E_SteerRunningRun_ResumesWithNoteWithoutFailure(t *testing.T) {
	sp := &steerProvider{first: make(chan struct{})}
	h := newE2EHarness(t, &fakeProvider{steps: []fakeStep{{}}})
	h.disp.ProviderFactory = func(AgentConfig) Provider { return sp }
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReadyWithProvider(t, wfID, "claude", 1)

	<-sp.first
	running := h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.ActiveAgentRunID != nil }, "run active")
	firstRunID := *running.ActiveAgentRunID
	h.pollAgentRun(t, firstRunID, func(r gen.AgentRun) bool { return r.Status == "running" }, "run running")

	// Sample the dispatch lock continuously: it must never be free between runs.
	var freeSeen atomic.Bool
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			tk, err := h.q.GetTask(context.Background(), taskID)
			if err == nil && tk.ActiveAgentRunID == nil && tk.Label == "ready" {
				freeSeen.Store(true)
			}
		}
	}()

	if !h.pool.Steer(firstRunID, SteerRequest{Message: "use approach B", Actor: "alice"}) {
		t.Fatal("Steer returned false for a running run")
	}
	h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.Label == "next" }, "follow-up run to complete")
	// Stop sampling only once the second run has taken and released the lock;
	// the check above covers the hand-over window.
	close(stop)
	<-done
	if freeSeen.Load() {
		t.Error("dispatch lock was free between the steered run and its follow-up")
	}

	first, _ := h.q.GetAgentRun(context.Background(), firstRunID)
	if first.Status != "interrupted" {
		t.Errorf("steered run status = %q, want interrupted", first.Status)
	}
	task, _ := h.q.GetTask(context.Background(), taskID)
	if task.Paused != 0 || task.ConsecutiveFailureCount != 0 || task.TransientRetryCount != 0 {
		t.Errorf("steer must not pause or count a failure: paused=%d failures=%d transient=%d", task.Paused, task.ConsecutiveFailureCount, task.TransientRetryCount)
	}

	sp.mu.Lock()
	defer sp.mu.Unlock()
	if len(sp.inputs) != 2 {
		t.Fatalf("provider invocations = %d, want 2", len(sp.inputs))
	}
	in := sp.inputs[1]
	if in.SteeringNote == nil || *in.SteeringNote != "use approach B" {
		t.Errorf("SteeringNote = %v", in.SteeringNote)
	}
	if in.ResumeSessionID != "sess-steer" {
		t.Errorf("ResumeSessionID = %q, want sess-steer", in.ResumeSessionID)
	}

	decs, err := h.q.ListTaskHumanDecisions(context.Background(), taskID)
	if err != nil || len(decs) != 1 || decs[0].Reply == nil || *decs[0].Reply != "use approach B" {
		t.Fatalf("steering note not recorded as a decision: %v %v", decs, err)
	}

	// The follow-up run's timeline opens with the note so the UI shows what
	// redirected it.
	runs, err := h.q.ListAgentRuns(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	var followUpID string
	for _, r := range runs {
		if r.ID != firstRunID {
			followUpID = r.ID
		}
	}
	if followUpID == "" {
		t.Fatalf("no follow-up run found among %d runs", len(runs))
	}
	logs, err := h.q.ListAgentLogs(context.Background(), followUpID)
	if err != nil {
		t.Fatalf("list follow-up logs: %v", err)
	}
	var sawNote bool
	for _, l := range logs {
		if l.Type == "system" && l.Content == "Steering note from human: use approach B" {
			sawNote = true
		}
	}
	if !sawNote {
		t.Errorf("follow-up run %s has no steering-note log entry; logs=%v", followUpID, logs)
	}
	firstLogs, _ := h.q.ListAgentLogs(context.Background(), firstRunID)
	for _, l := range firstLogs {
		if strings.HasPrefix(l.Content, "Steering note from human:") {
			t.Errorf("steering note logged on the interrupted run instead of the follow-up")
		}
	}
}
