package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// TestDispatchSweep_OneHungWorktree_DoesNotBlockOtherTasks is the regression
// test for issue #246: dispatch used to shell out to git with no timeout, so
// a stalled remote on one repo would hang that git call forever, blocking the
// dispatcher's serial sweep loop and starving every other task/repo. With the
// gitRunner seam + gitTimeout bound added in worktree.go, a hung repo's git
// call now fails within the configured timeout (wrapped as *ErrTransient)
// while a healthy repo's task in the same sweep still dispatches normally.
func TestDispatchSweep_OneHungWorktree_DoesNotBlockOtherTasks(t *testing.T) {
	// Bound the whole test so a regression (the hang coming back) fails fast
	// instead of blocking `go test` indefinitely.
	testDone := make(chan struct{})
	go func() {
		defer close(testDone)
		runHungWorktreeTest(t)
	}()
	select {
	case <-testDone:
	case <-time.After(20 * time.Second):
		t.Fatal("test did not complete within 20s — dispatch sweep likely hung on the bad repo's git call")
	}
}

func runHungWorktreeTest(t *testing.T) {
	t.Helper()

	// Two real git repos: "good" behaves normally, "bad" is the one whose git
	// calls we'll force to hang via the gitRunner seam.
	goodRepo := initRepo(t)
	badRepo := initRepo(t)

	// Configure a short timeout and a stub runner that hangs (until ctx is
	// cancelled) for anything under badRepo, delegating to the real runGit
	// for goodRepo. Restore both package-level seams afterward — they're
	// global mutable state shared with every other test in this package.
	origTimeout := gitTimeout
	origRunner := gitRunner
	t.Cleanup(func() {
		gitTimeout = origTimeout
		gitRunner = origRunner
	})
	SetGitTimeout(200 * time.Millisecond)
	gitRunner = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if dir == badRepo {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return runGit(ctx, dir, args...)
	}

	f, err := os.CreateTemp("", "dispatcher-hung-worktree-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	q := gen.New(db.SQL())

	// A minimal workflow with one agent-pickup label ("ready") — mirrors
	// dispatch_e2e_test.go's seedE2EWorkflow so both tasks below are eligible
	// for dispatch.
	wfID := uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "hung-worktree-test", Description: "regression for #246"}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wfID, Name: "ready", Color: "#000000", SortOrder: 0,
	}); err != nil {
		t.Fatalf("create label: %v", err)
	}
	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wfID, Name: "next", Color: "#000000", SortOrder: 1,
	}); err != nil {
		t.Fatalf("create label: %v", err)
	}
	successLabel := "success"
	if _, err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
		ID: uuid.NewString(), WorkflowID: wfID, FromLabel: "ready", ToLabel: "next", TriggerType: "agent", Path: &successLabel,
	}); err != nil {
		t.Fatalf("create transition: %v", err)
	}

	goodRepoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: goodRepoID, Name: "good", Path: goodRepo, WorkflowID: &wfID}); err != nil {
		t.Fatalf("create good repo: %v", err)
	}
	badRepoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: badRepoID, Name: "bad", Path: badRepo, WorkflowID: &wfID}); err != nil {
		t.Fatalf("create bad repo: %v", err)
	}

	pcID := uuid.NewString()
	if _, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{
		ID: pcID, Name: "test-provider", Provider: "fake", Model: "none", Env: `{}`,
	}); err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	if _, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "fake-agent", ProviderConfigID: pcID,
		Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1,
	}); err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	goodTaskID := uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: goodTaskID, Title: "good task", WorkflowID: wfID, RepoID: goodRepoID, Label: "ready",
	}); err != nil {
		t.Fatalf("create good task: %v", err)
	}
	badTaskID := uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: badTaskID, Title: "bad task", WorkflowID: wfID, RepoID: badRepoID, Label: "ready",
	}); err != nil {
		t.Fatalf("create bad task: %v", err)
	}

	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	pub := &recordingPub{}
	engine := workflow.New(db.SQL(), pub)
	pool := NewPool(2, db.SQL(), engine, pub)
	pool.GitName, pool.GitEmail = "Test", "test@example.com"
	factory := func(AgentConfig) Provider { return fp }
	d := NewDispatcher(db.SQL(), pool, engine, factory)
	d.Publisher = pub

	poolCtx, poolCancel := context.WithCancel(context.Background())
	poolDone := make(chan struct{})
	go func() { defer close(poolDone); pool.Start(poolCtx) }()
	t.Cleanup(func() {
		poolCancel()
		<-poolDone
	})

	// Run one sweep directly (not via Run's ticker loop) with a deadline that
	// must comfortably exceed the bad repo's forced timeout (200ms) — if the
	// sweep still blocks on the hung repo, this call itself will hang and the
	// outer 20s test guard will fire.
	sweepCtx, sweepCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer sweepCancel()
	d.sweep(sweepCtx)

	// The good task must have been dispatched despite the bad repo's task
	// being processed in the same sweep. The fake provider completes
	// synchronously, so by the time we observe it the task may have already
	// run to completion and transitioned off "ready" (active_agent_run_id
	// cleared again) — either an active run or having left "ready" entirely
	// is proof dispatch succeeded, so also check for at least one run row.
	deadline := time.Now().Add(5 * time.Second)
	var goodTask gen.Task
	for time.Now().Before(deadline) {
		goodTask, err = q.GetTask(ctx, goodTaskID)
		if err == nil && (goodTask.ActiveAgentRunID != nil || goodTask.Label != "ready") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if goodTask.ActiveAgentRunID == nil && goodTask.Label == "ready" {
		t.Fatalf("expected good task to be dispatched despite the bad repo's git hang, got %+v", goodTask)
	}
	runs, err := q.ListAgentRuns(ctx, goodTaskID)
	if err != nil {
		t.Fatalf("list agent runs for good task: %v", err)
	}
	if len(runs) == 0 {
		t.Fatalf("expected at least one agent run created for the good task, got 0")
	}

	// The bad task must NOT have been dispatched — its worktree provisioning
	// failed on the forced git timeout, so dispatch logged the error and moved on.
	badTask, err := q.GetTask(ctx, badTaskID)
	if err != nil {
		t.Fatalf("get bad task: %v", err)
	}
	if badTask.ActiveAgentRunID != nil {
		t.Fatalf("expected bad task to remain un-dispatched after its git call timed out, got active run %q", *badTask.ActiveAgentRunID)
	}
}
