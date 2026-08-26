package agent

import (
	"context"
	"strings"
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

// TestE2E_RuntimeImageSkipsDevcontainerResolution is resolution-order case 1
// exercised through the real dispatcher: a repo with BOTH runtime_image and
// devcontainer_json set must resolve via the explicit-image path only. If
// devcontainer resolution were even attempted here, EnsureDevcontainerRunning
// would try to shell out to the (not installed under `go test`) devcontainer
// CLI and this test would fail/hang instead of cleanly escalating via the
// nil-Runtime path — which is exactly the observable signal this test uses:
// with Dispatcher.Runtime nil, only ONE runtime-unavailable error message
// should ever be produced (the runtime_image one), never a devcontainer one,
// and the devcontainer CLI must never be invoked.
func TestE2E_RuntimeImageSkipsDevcontainerResolution(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	// h.disp.Runtime intentionally left nil, same as
	// TestE2E_RuntimeImageSetWithoutManagerEscalates — if this test's task
	// escalated for the *devcontainer* reason instead of the runtime_image
	// reason, or hung trying to shell out to a real devcontainer CLI, that
	// would prove devcontainer resolution ran despite runtime_image being set.
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReadyRuntimeImageAndDevcontainer(t, wfID, "ghcr.io/example/runtime:1", `{"image":"golang:1.26"}`)

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
	if run.Notes == nil || !strings.Contains(*run.Notes, "runtime_image") {
		t.Errorf("expected escalation to reference the runtime_image path, got notes: %v", run.Notes)
	}
	if run.Notes != nil && strings.Contains(*run.Notes, "devcontainer") {
		t.Errorf("expected no devcontainer-path escalation when runtime_image is set, got notes: %v", *run.Notes)
	}

	if len(fp.inputs) != 0 {
		t.Errorf("expected the provider to never be invoked when the runtime container can't be resolved, got %d invocation(s)", len(fp.inputs))
	}
}

// TestE2E_DevcontainerConfigWithoutManagerEscalates is
// TestE2E_RuntimeImageSetWithoutManagerEscalates for the devcontainer path
// (resolution-order case 3, DB-stored devcontainer_json, no runtime_image):
// a repo with a devcontainer source configured but no Dispatcher.Runtime
// escalates the same way instead of hot-looping or silently running
// in-process against a toolchain the task was never meant to use.
func TestE2E_DevcontainerConfigWithoutManagerEscalates(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	// h.disp.Runtime intentionally left nil.
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReadyDevcontainer(t, wfID, `{"image":"golang:1.26"}`)

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
		t.Errorf("expected the provider to never be invoked when the devcontainer runtime can't be resolved, got %d invocation(s)", len(fp.inputs))
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

// seedTaskOnReadyRuntimeImageAndDevcontainer is seedTaskOnReadyRuntimeImage
// with the repo's DevcontainerJson *also* set, for
// TestE2E_RuntimeImageSkipsDevcontainerResolution — proving an explicit
// runtime_image wins over a devcontainer source (resolution-order case 1)
// rather than the two being merged or the devcontainer path being consulted
// at all.
func (h *e2eHarness) seedTaskOnReadyRuntimeImageAndDevcontainer(t *testing.T, wfID, image, devcontainerJSON string) string {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID,
		RuntimeImage: image, DevcontainerJson: devcontainerJSON,
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

// seedTaskOnReadyDevcontainer is seedTaskOnReady with the repo's
// devcontainer_json set (and no runtime_image), for exercising the
// EnsureDevcontainerRunning dispatch path (resolution-order case 3 — no
// repo-committed .devcontainer/devcontainer.json exists in h.repo's fixture,
// so the DB-stored config is what wins here).
func (h *e2eHarness) seedTaskOnReadyDevcontainer(t *testing.T, wfID, devcontainerJSON string) string {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID, DevcontainerJson: devcontainerJSON,
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
