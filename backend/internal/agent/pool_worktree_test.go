package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent/runtime"
)

// inputCapturingProvider records the RunInput it was invoked with.
type inputCapturingProvider struct {
	calls int
	input RunInput
}

func (p *inputCapturingProvider) Run(_ context.Context, in RunInput, _ chan<- LogEntry) (Result, error) {
	p.calls++
	p.input = in
	return Result{Status: "completed", Outcome: "success"}, nil
}

// TestPoolSetupWorktree_RunsInWorkerBeforePrepAndProvider verifies that
// Submit never runs the worktree setup (the dispatcher's sweep calls Submit),
// that the worker runs it before toolchain prep and the provider, and that the
// provisioned path/branch reach both prep and the provider input.
func TestPoolSetupWorktree_RunsInWorkerBeforePrepAndProvider(t *testing.T) {
	orig := runtimePrep
	defer func() { runtimePrep = orig }()
	var order []string
	var prepDir string
	runtimePrep = func(_ context.Context, _ []runtime.Pin, dir string) error {
		order = append(order, "prep")
		prepDir = dir
		return nil
	}

	pool, q, _ := newRuntimePoolTestDB(t)
	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedRuntimeJobFixtures(t, q, wfs[0].ID)

	wtDir := t.TempDir()
	provider := &inputCapturingProvider{}
	job := Job{
		RunID:    runID,
		Provider: provider,
		SetupWorktree: func(context.Context) (WorktreeResult, error) {
			order = append(order, "setup")
			return WorktreeResult{Path: wtDir, Branch: "task/x"}, nil
		},
		Input: RunInput{
			RunID:       runID,
			Task:        Task{ID: taskID, Title: "t", Label: "plan", WorkflowID: wfs[0].ID},
			AgentConfig: AgentConfig{ID: agCfgID, Name: "mock-agent", Provider: "mock"},
			Runtime:     &RuntimeSpec{Pins: []runtime.Pin{{ID: "go", Version: "1.21"}}},
		},
	}

	// Submit only enqueues; the pool has no workers started, so setup must not
	// have run on the caller's goroutine.
	if !pool.Submit(job) {
		t.Fatal("submit failed")
	}
	if len(order) != 0 {
		t.Fatalf("Submit must not run worktree setup, got %v", order)
	}

	pool.run(context.Background(), job)

	if len(order) != 2 || order[0] != "setup" || order[1] != "prep" {
		t.Errorf("order = %v, want [setup prep]", order)
	}
	if prepDir != wtDir {
		t.Errorf("runtime prep worktree = %q, want %q", prepDir, wtDir)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if provider.input.RepoPath != wtDir || provider.input.Task.Branch != "task/x" || provider.input.Runtime.WorktreeDir != wtDir {
		t.Errorf("provider input not updated from setup: repo=%q branch=%q rt=%+v", provider.input.RepoPath, provider.input.Task.Branch, provider.input.Runtime)
	}
}

// TestPoolSetupWorktree_FailureEscalatesWithoutProvider verifies a setup
// failure never reaches the provider and is handled as a transient failure;
// with no retry budget configured it escalates and keeps the dispatch lock.
func TestPoolSetupWorktree_FailureEscalatesWithoutProvider(t *testing.T) {
	pool, q, pub := newRuntimePoolTestDB(t)
	ctx := context.Background()
	wfs, _ := q.ListWorkflows(ctx)
	taskID, agCfgID, runID := seedRuntimeJobFixtures(t, q, wfs[0].ID)

	provider := &inputCapturingProvider{}
	job := Job{
		RunID:    runID,
		Provider: provider,
		SetupWorktree: func(context.Context) (WorktreeResult, error) {
			return WorktreeResult{}, errors.New("fetch: remote unreachable")
		},
		Input: RunInput{
			RunID:       runID,
			Task:        Task{ID: taskID, Title: "t", Label: "plan", WorkflowID: wfs[0].ID},
			AgentConfig: AgentConfig{ID: agCfgID, Name: "mock-agent", Provider: "mock"},
		},
	}
	pool.run(ctx, job)

	if provider.calls != 0 {
		t.Fatalf("provider must not run after worktree setup failure, got %d calls", provider.calls)
	}
	run, err := q.GetAgentRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	// max_retries defaults to 0 => the transient budget is exhausted at once.
	if run.Status != "waiting_human" || run.EscalationReason == nil || *run.EscalationReason != string(ReasonTransientExhausted) {
		t.Errorf("run = status %q reason %v, want waiting_human/%s", run.Status, run.EscalationReason, ReasonTransientExhausted)
	}
	if run.Notes == nil || !strings.Contains(*run.Notes, "worktree setup failed") {
		t.Errorf("run notes should mention the setup failure, got %v", run.Notes)
	}
	task, _ := q.GetTask(ctx, taskID)
	if task.ActiveAgentRunID == nil || *task.ActiveAgentRunID != runID {
		t.Errorf("escalated run must keep the dispatch lock, got %v", task.ActiveAgentRunID)
	}
	if !pub.hasEvent("task.needs_human") {
		t.Error("expected task.needs_human")
	}
}
