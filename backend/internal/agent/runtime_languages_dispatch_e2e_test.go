package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestE2E_RuntimeLanguagesSetWithoutManagerEscalates mirrors
// TestE2E_RuntimeImageSetWithoutManagerEscalates for resolution order step 3
// (repos.runtime_languages, the picker): a repo with a valid language list
// but no Dispatcher.Runtime configured escalates to waiting_human instead of
// hot-looping or silently falling back to in-process.
func TestE2E_RuntimeLanguagesSetWithoutManagerEscalates(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	// h.disp.Runtime intentionally left nil.
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReadyRuntimeLanguages(t, wfID, `[{"id":"go","version":"1.26"}]`)

	h.waitForRunStatus(t, taskID, "waiting_human")

	task, err := h.q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.ActiveAgentRunID == nil {
		t.Fatal("expected task to remain locked on the escalated run")
	}
	if len(fp.inputs) != 0 {
		t.Errorf("expected the provider to never be invoked when the runtime container can't be resolved, got %d invocation(s)", len(fp.inputs))
	}
}

// TestE2E_InvalidRuntimeLanguagesEscalates verifies a repo whose stored
// runtime_languages fails ParseRuntimeLanguages (defense in depth against a
// row that somehow bypassed handler-level validation) escalates the same
// way as an unresolvable runtime container, rather than panicking or
// silently running in-process.
func TestE2E_InvalidRuntimeLanguagesEscalates(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReadyRuntimeLanguages(t, wfID, `[{"id":"cobol","version":"1"}]`)

	h.waitForRunStatus(t, taskID, "waiting_human")

	if len(fp.inputs) != 0 {
		t.Errorf("expected the provider to never be invoked for an invalid runtime_languages row, got %d invocation(s)", len(fp.inputs))
	}
}

// TestE2E_RepoDevcontainerFileWinsOverRuntimeLanguages verifies resolution
// order step 2 beats step 3: when a repo has BOTH a committed
// .devcontainer/devcontainer.json AND repos.runtime_languages set, dispatch
// takes the repo-file path (EnsureDevcontainerRunningFromFile) rather than
// the generated-language path. Both paths need a RuntimeManager to actually
// run `devcontainer up`, which isn't available hermetically — so this test
// only asserts precedence via the escalation error, which differs between
// the two paths ("committed devcontainer.json" vs "runtime_languages set").
func TestE2E_RepoDevcontainerFileWinsOverRuntimeLanguages(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)

	if err := os.MkdirAll(filepath.Join(h.repo, ".devcontainer"), 0o755); err != nil {
		t.Fatalf("mkdir .devcontainer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(h.repo, devcontainerFilePath), []byte(`{"image":"golang:1.26"}`), 0o644); err != nil {
		t.Fatalf("write devcontainer.json: %v", err)
	}

	taskID := h.seedTaskOnReadyRuntimeLanguages(t, wfID, `[{"id":"go","version":"1.26"}]`)

	run := h.waitForRunStatus(t, taskID, "waiting_human")
	note := ""
	if run.Notes != nil {
		note = *run.Notes
	}
	if !containsSubstring([]string{note}, "committed devcontainer.json") {
		t.Errorf("expected escalation note to mention the repo-committed devcontainer.json path (step 2 winning over step 3), got %q", note)
	}
}

// seedTaskOnReadyRuntimeLanguages is seedTaskOnReady with the repo's
// runtime_languages set, for exercising the generated-devcontainer dispatch
// path.
func (h *e2eHarness) seedTaskOnReadyRuntimeLanguages(t *testing.T, wfID, runtimeLanguages string) string {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID, RuntimeLanguages: runtimeLanguages,
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

// waitForRunStatus polls until the task's active run reaches wantStatus,
// then gives the sweeper a few more ticks to prove the lock holds (mirrors
// TestE2E_RuntimeImageSetWithoutManagerEscalates's inline polling, factored
// out since this file needs it twice).
func (h *e2eHarness) waitForRunStatus(t *testing.T, taskID, wantStatus string) gen.AgentRun {
	t.Helper()
	locked := h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.ActiveAgentRunID != nil }, "run to be created and task locked")
	runID := *locked.ActiveAgentRunID

	deadline := time.Now().Add(5 * time.Second)
	var run gen.AgentRun
	for time.Now().Before(deadline) {
		var err error
		run, err = h.q.GetAgentRun(context.Background(), runID)
		if err == nil && run.Status == wantStatus {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.Status != wantStatus {
		t.Fatalf("expected run to reach status %q, got %q", wantStatus, run.Status)
	}

	// A few more ticks to prove the lock actually prevents re-dispatch.
	time.Sleep(150 * time.Millisecond)
	return run
}
