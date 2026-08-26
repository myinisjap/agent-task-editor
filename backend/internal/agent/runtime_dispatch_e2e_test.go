package agent

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestE2E_EmptyRuntimeImageRunsInProcess is the non-negotiable regression
// guard from runtime-images.md's T3 scope: a repo with no runtime_image set
// (the default, and every repo that existed before this feature) must see
// RunInput.RuntimeContainer as "" — exactly as before RuntimeManager
// existed — with no dependency on Dispatcher.Runtime being configured at
// all. This proves the strictly-additive claim end-to-end through the real
// dispatcher, not just at the spawn()/argv level already covered by
// providers/cli_test.go.
func TestE2E_EmptyRuntimeImageRunsInProcess(t *testing.T) {
	step := fakeStep{result: Result{Status: "completed", Outcome: "success"}}
	fp := &fakeProvider{steps: []fakeStep{step}}
	h := newE2EHarness(t, fp)
	// Deliberately leave h.disp.Runtime nil — the point of this test is that
	// nothing about the empty-runtime_image path requires it.
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReady(t, wfID) // seeds a repo with runtime_image left at its "" default

	h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.Label == "next" }, "task to reach terminal 'next' label")

	got := fp.input(t, 0)
	if got.RuntimeContainer != "" {
		t.Errorf("expected RuntimeContainer to stay empty for a repo with no runtime_image, got %q", got.RuntimeContainer)
	}
}

// TestE2E_RuntimeImageSetWithoutManagerEscalates verifies a repo that
// declares runtime_image but has no Dispatcher.Runtime configured fails the
// same way the nil-provider case does (escalateStartRunFailure): the task
// stays locked on a waiting_human run instead of hot-looping re-dispatch,
// and the provider is never invoked with a bogus/empty RuntimeContainer that
// would silently run in-process against the wrong environment.
func TestE2E_RuntimeImageSetWithoutManagerEscalates(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	// h.disp.Runtime intentionally left nil.
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReadyRuntimeImage(t, wfID, "ghcr.io/example/runtime:1")

	locked := h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.ActiveAgentRunID != nil }, "run to be created and task locked")
	runID := *locked.ActiveAgentRunID

	deadline := time.Now().Add(5 * time.Second)
	var run gen.AgentRun
	for time.Now().Before(deadline) {
		var err error
		run, err = h.q.GetAgentRun(context.Background(), runID)
		if err == nil && run.Status == "waiting_human" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.Status != "waiting_human" {
		t.Fatalf("expected run to escalate to waiting_human, got %q", run.Status)
	}

	// Give the sweeper several more ticks to prove the lock actually
	// prevents re-dispatch (mirrors TestE2E_NilProviderFailsRunCleanly).
	time.Sleep(150 * time.Millisecond)

	task, err := h.q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.ActiveAgentRunID == nil {
		t.Fatal("expected task to remain locked on the escalated run")
	}
	runs, err := h.q.ListAgentRuns(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected exactly one run (the lock should have prevented re-dispatch), got %d", len(runs))
	}

	if len(fp.inputs) != 0 {
		t.Errorf("expected the provider to never be invoked when the runtime container can't be resolved, got %d invocation(s)", len(fp.inputs))
	}
}

// seedTaskOnReadyRuntimeImage is seedTaskOnReady with the repo's
// runtime_image set, for exercising the RuntimeManager dispatch path.
func (h *e2eHarness) seedTaskOnReadyRuntimeImage(t *testing.T, wfID, image string) string {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID, RuntimeImage: image,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	pcID := h.createProviderConfig(t, "fake", "none")
	if _, err := h.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "fake-agent", ProviderConfigID: pcID,
		Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1,
	}); err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	taskID := uuid.NewString()
	if _, err := h.q.CreateTask(ctx, gen.CreateTaskParams{
		ID: taskID, Title: "do the thing", WorkflowID: wfID, RepoID: repoID, Label: "ready",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return taskID
}
