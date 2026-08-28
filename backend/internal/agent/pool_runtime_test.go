package agent

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent/runtime"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// runtimeTestPub is a minimal Publisher recorder, mirroring pool_test.go's
// testPub (unavailable here since this file is package agent, not
// agent_test, so it can reach the unexported runtimePrep seam below).
type runtimeTestPub struct {
	events []string
}

func (p *runtimeTestPub) Publish(eventType string, _ map[string]any) {
	p.events = append(p.events, eventType)
}

func (p *runtimeTestPub) hasEvent(name string) bool {
	for _, e := range p.events {
		if e == name {
			return true
		}
	}
	return false
}

// mockRunProvider returns a pre-configured Result immediately. Mirrors
// pool_test.go's mockProvider.
type mockRunProvider struct {
	result Result
}

func (p *mockRunProvider) Run(_ context.Context, _ RunInput, _ chan<- LogEntry) (Result, error) {
	return p.result, nil
}

// newRuntimePoolTestDB opens a seeded temp SQLite DB and returns the pool
// plus the raw queries for assertions, wired the same way pool_test.go's
// openAgentTestDB + agent.NewPool are.
func newRuntimePoolTestDB(t *testing.T) (*Pool, *gen.Queries, *runtimeTestPub) {
	t.Helper()
	f, err := os.CreateTemp("", "pool-runtime-*.db")
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

	if err := storage.SeedDefaultWorkflow(context.Background(), db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	pub := &runtimeTestPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := NewPool(1, db.SQL(), engine, pub)
	return pool, q, pub
}

// seedRuntimeJobFixtures creates the minimum DB rows a pool run needs: repo,
// task, agent config, agent run — and locks the task's active_agent_run_id
// on it, mirroring a real dispatch (dispatcher.persistRunRow).
func seedRuntimeJobFixtures(t *testing.T, q *gen.Queries, wfID string) (taskID, agCfgID, runID string) {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: t.TempDir(), WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	taskID = uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: taskID, Title: "Runtime prep test task", WorkflowID: wfID, RepoID: repoID, Label: "plan",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	pc, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{
		ID: uuid.NewString(), Name: "mock-provider", Provider: "mock", Model: "none", Env: `{}`,
	})
	if err != nil {
		t.Fatalf("create provider config: %v", err)
	}

	agCfgID = uuid.NewString()
	if _, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: agCfgID, Name: "mock-agent", ProviderConfigID: pc.ID, Labels: `["plan"]`,
	}); err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	runID = uuid.NewString()
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID: runID, TaskID: taskID, AgentConfigID: &agCfgID,
	}); err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID, ActiveAgentRunID: &runID, ID: taskID,
	}); err != nil {
		t.Fatalf("seed active run: %v", err)
	}
	return
}

// TestPoolPrepareRuntime_NilSpecIsPassthrough is the §4.1 byte-identical
// guard at the pool layer: a job with no RuntimeSpec must never invoke
// runtimePrep.
func TestPoolPrepareRuntime_NilSpecIsPassthrough(t *testing.T) {
	orig := runtimePrep
	defer func() { runtimePrep = orig }()
	called := false
	runtimePrep = func(context.Context, []runtime.Pin, string) error {
		called = true
		return nil
	}

	pool, q, _ := newRuntimePoolTestDB(t)
	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedRuntimeJobFixtures(t, q, wfs[0].ID)

	provider := &mockRunProvider{result: Result{Status: "completed", Outcome: "success"}}
	job := Job{
		RunID:    runID,
		Provider: provider,
		Input: RunInput{
			RunID:       runID,
			Task:        Task{ID: taskID, Title: "t", Label: "plan", WorkflowID: wfs[0].ID},
			AgentConfig: AgentConfig{ID: agCfgID, Name: "mock-agent", Provider: "mock"},
			RepoPath:    t.TempDir(),
			// Runtime deliberately nil.
		},
	}

	pool.run(context.Background(), job)

	if called {
		t.Error("expected runtimePrep to never be called for a job with a nil RuntimeSpec")
	}
	run, err := q.GetAgentRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "completed" {
		t.Errorf("run status = %q, want completed", run.Status)
	}
}

// TestPoolPrepareRuntime_SuccessInvokesProvider verifies a job with a
// RuntimeSpec calls runtimePrep before the provider runs, and on success the
// provider still executes normally.
func TestPoolPrepareRuntime_SuccessInvokesProvider(t *testing.T) {
	orig := runtimePrep
	defer func() { runtimePrep = orig }()
	var gotPins []runtime.Pin
	var gotWorktree string
	runtimePrep = func(_ context.Context, pins []runtime.Pin, worktreeDir string) error {
		gotPins = pins
		gotWorktree = worktreeDir
		return nil
	}

	pool, q, _ := newRuntimePoolTestDB(t)
	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedRuntimeJobFixtures(t, q, wfs[0].ID)

	wtDir := t.TempDir()
	pins := []runtime.Pin{{ID: "go", Version: "1.21"}}
	provider := &mockRunProvider{result: Result{Status: "completed", Outcome: "success"}}
	job := Job{
		RunID:    runID,
		Provider: provider,
		Input: RunInput{
			RunID:       runID,
			Task:        Task{ID: taskID, Title: "t", Label: "plan", WorkflowID: wfs[0].ID},
			AgentConfig: AgentConfig{ID: agCfgID, Name: "mock-agent", Provider: "mock"},
			RepoPath:    wtDir,
			Runtime:     &RuntimeSpec{Pins: pins, WorktreeDir: wtDir},
		},
	}

	pool.run(context.Background(), job)

	if len(gotPins) != 1 || gotPins[0].ID != "go" {
		t.Errorf("runtimePrep pins = %+v, want [go@1.21]", gotPins)
	}
	if gotWorktree != wtDir {
		t.Errorf("runtimePrep worktree = %q, want %q", gotWorktree, wtDir)
	}

	run, err := q.GetAgentRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "completed" {
		t.Errorf("run status = %q, want completed (provider should have run after successful prep)", run.Status)
	}
}

// TestPoolPrepareRuntime_FailureEscalatesToWaitingHuman verifies the
// fail-closed contract (PRD §4.6) from the pool layer: a runtimePrep failure
// marks the run waiting_human, leaves active_agent_run_id locked on this run
// (never re-dispatched with a plain, unprepared spawn), publishes
// task.needs_human, and — critically — never invokes the provider.
func TestPoolPrepareRuntime_FailureEscalatesToWaitingHuman(t *testing.T) {
	orig := runtimePrep
	defer func() { runtimePrep = orig }()
	runtimePrep = func(context.Context, []runtime.Pin, string) error {
		return errors.New("fake mise install failure: exit status 1")
	}

	pool, q, pub := newRuntimePoolTestDB(t)
	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedRuntimeJobFixtures(t, q, wfs[0].ID)

	providerCalled := false
	provider := &recordingProvider{onRun: func() { providerCalled = true }}
	wtDir := t.TempDir()
	job := Job{
		RunID:    runID,
		Provider: provider,
		Input: RunInput{
			RunID:       runID,
			Task:        Task{ID: taskID, Title: "t", Label: "plan", WorkflowID: wfs[0].ID},
			AgentConfig: AgentConfig{ID: agCfgID, Name: "mock-agent", Provider: "mock"},
			RepoPath:    wtDir,
			Runtime:     &RuntimeSpec{Pins: []runtime.Pin{{ID: "python", Version: "9.9.9"}}, WorktreeDir: wtDir},
		},
	}

	pool.run(context.Background(), job)

	if providerCalled {
		t.Error("expected the provider to never run after a runtime prep failure")
	}

	run, err := q.GetAgentRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "waiting_human" {
		t.Errorf("run status = %q, want waiting_human", run.Status)
	}
	if run.Notes == nil {
		t.Fatal("expected run notes to be set with the prep error")
	}

	task, err := q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.ActiveAgentRunID == nil || *task.ActiveAgentRunID != runID {
		t.Errorf("active_agent_run_id = %v, want locked on %q", task.ActiveAgentRunID, runID)
	}
	if !pub.hasEvent("task.needs_human") {
		t.Error("expected task.needs_human event")
	}
}

// recordingProvider calls onRun and returns a completed result — used to
// prove the provider was (or wasn't) invoked.
type recordingProvider struct {
	onRun func()
}

func (p *recordingProvider) Run(_ context.Context, _ RunInput, _ chan<- LogEntry) (Result, error) {
	p.onRun()
	return Result{Status: "completed", Outcome: "success"}, nil
}

// TestSweepDispatch_DoesNotBlockOnRuntimePrep is the core regression guard
// for this fix: a slow (or hanging) runtime prep must run in the job's own
// pool goroutine, never in the dispatcher's synchronous startRun/sweep path.
// It submits a job whose runtimePrep blocks until released, then asserts
// Dispatcher.startRun (which calls resolveRuntimeSpec, not runtimePrep)
// returns immediately without waiting for prep to finish.
func TestSweepDispatch_DoesNotBlockOnRuntimePrep(t *testing.T) {
	orig := runtimePrep
	defer func() { runtimePrep = orig }()
	release := make(chan struct{})
	prepStarted := make(chan struct{})
	runtimePrep = func(ctx context.Context, _ []runtime.Pin, _ string) error {
		close(prepStarted)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer close(release)

	f, err := os.CreateTemp("", "dispatcher-runtime-sweep-*.db")
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
	if err := storage.SeedDefaultWorkflow(context.Background(), db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	q := gen.New(db.SQL())
	pub := &runtimeTestPub{}
	engine := workflow.New(db.SQL(), pub)
	pool := NewPool(1, db.SQL(), engine, pub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pool.Start(ctx)

	wfs, _ := q.ListWorkflows(context.Background())
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: initRepo(t), WorkflowID: &wfs[0].ID,
		RuntimeLanguages: `[{"id":"go","version":"1.21"}]`,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	taskID := uuid.NewString()
	if _, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: taskID, Title: "sweep test", WorkflowID: wfs[0].ID, RepoID: repoID, Label: "plan",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	pc, err := q.CreateProviderConfig(context.Background(), gen.CreateProviderConfigParams{
		ID: uuid.NewString(), Name: "mock-provider", Provider: "mock", Model: "none", Env: `{}`,
	})
	if err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	agCfgID := uuid.NewString()
	if _, err := q.CreateAgentConfig(context.Background(), gen.CreateAgentConfigParams{
		ID: agCfgID, Name: "mock-agent", ProviderConfigID: pc.ID, Labels: `["plan"]`,
	}); err != nil {
		t.Fatalf("create agent config: %v", err)
	}
	matched, err := q.GetAgentConfig(context.Background(), agCfgID)
	if err != nil {
		t.Fatalf("get agent config: %v", err)
	}

	d := NewDispatcher(db.SQL(), pool, engine, func(AgentConfig) Provider {
		return &mockRunProvider{result: Result{Status: "completed", Outcome: "success"}}
	})

	task, err := q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}

	done := make(chan struct{})
	var startErr error
	go func() {
		defer close(done)
		_, startErr = d.startRun(context.Background(), task, matched, runOptions{})
	}()

	select {
	case <-done:
		if startErr != nil {
			t.Fatalf("startRun failed: %v", startErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startRun (sweep dispatch path) blocked on runtime prep — it should return as soon as the job is submitted to the pool")
	}

	// Confirm prep actually did start in the pool's goroutine (proves the
	// non-blocking result above isn't just "prep never ran").
	select {
	case <-prepStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("expected runtimePrep to have been invoked from the pool's goroutine")
	}
}
