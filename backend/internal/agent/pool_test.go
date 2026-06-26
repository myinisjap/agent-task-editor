package agent_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// testPub records published event types.
type testPub struct {
	events []string
}

func (p *testPub) Publish(eventType string, _ map[string]any) {
	p.events = append(p.events, eventType)
}

// mockProvider returns a pre-configured Result immediately.
type mockProvider struct {
	result agent.Result
	err    error
}

func (p *mockProvider) Run(_ context.Context, _ agent.RunInput, _ chan<- agent.LogEntry) (agent.Result, error) {
	return p.result, p.err
}

// openAgentTestDB opens a seeded temp SQLite database.
func openAgentTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp("", "agent-test-*.db")
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
	return db
}

// seedJobFixtures creates the minimum DB rows needed for a pool job:
// workflow (already seeded), repo, task, agent config, and agent run.
func seedJobFixtures(t *testing.T, q *gen.Queries, wfID string) (taskID, agCfgID, runID string) {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	_, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID:         repoID,
		Name:       "repo",
		Path:       t.TempDir(),
		WorkflowID: &wfID,
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	taskID = uuid.NewString()
	_, err = q.CreateTask(ctx, gen.CreateTaskParams{
		ID:         taskID,
		Title:      "Pool test task",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "todo",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	agCfgID = uuid.NewString()
	_, err = q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID:       agCfgID,
		Name:     "mock-agent",
		Provider: "mock",
		Model:    "none",
		Labels:   `["todo"]`,
		Env:      `{}`,
	})
	if err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	runID = uuid.NewString()
	_, err = q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:            runID,
		TaskID:        taskID,
		AgentConfigID: &agCfgID,
	})
	if err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	return
}

func buildJob(runID, taskID, agCfgID, wfID, repoPath string, provider agent.Provider) agent.Job {
	return agent.Job{
		RunID:    runID,
		Provider: provider,
		Input: agent.RunInput{
			RunID: runID,
			Task:  agent.Task{ID: taskID, Title: "Pool test task", Label: "todo", WorkflowID: wfID},
			AgentConfig: agent.AgentConfig{
				ID:       agCfgID,
				Name:     "mock-agent",
				Provider: "mock",
			},
			RepoPath: repoPath,
		},
	}
}

// waitForStatus polls until the run has the expected status or times out.
func waitForStatus(t *testing.T, q *gen.Queries, runID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, err := q.GetAgentRun(context.Background(), runID)
		if err == nil && run.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	run, _ := q.GetAgentRun(context.Background(), runID)
	t.Errorf("timed out waiting for run %s to reach status %q; current: %q", runID, want, run.Status)
}

func TestPool_CompletedResult_TransitionsLabel(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	provider := &mockProvider{result: agent.Result{Status: "completed", Outcome: "success"}}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	pool.Submit(buildJob(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), provider))

	waitForStatus(t, q, runID, "completed")
	cancel()

	// Task label should have been transitioned
	task, _ := q.GetTask(context.Background(), taskID)
	if task.Label != "in-progress" {
		t.Errorf("expected label 'in-progress', got %q", task.Label)
	}

	// Events: agent_started + agent_done (label_changed also fired by engine)
	hasEvent := func(name string) bool {
		for _, e := range pub.events {
			if e == name {
				return true
			}
		}
		return false
	}
	if !hasEvent("task.agent_started") {
		t.Errorf("expected task.agent_started event; got %v", pub.events)
	}
	if !hasEvent("task.agent_done") {
		t.Errorf("expected task.agent_done event; got %v", pub.events)
	}
}

func TestPool_FailedResult_SetsStatusFailed(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	provider := &mockProvider{result: agent.Result{Status: "failed"}}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	pool.Submit(buildJob(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), provider))

	waitForStatus(t, q, runID, "failed")
	cancel()

	run, _ := q.GetAgentRun(context.Background(), runID)
	if run.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", run.Status)
	}
}

func TestPool_WaitingHuman_PublishesEvent(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	msg := "need approval"
	provider := &mockProvider{result: agent.Result{Status: "waiting_human", Message: &msg}}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	pool.Submit(buildJob(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), provider))

	waitForStatus(t, q, runID, "waiting_human")
	cancel()

	hasNeedsHuman := false
	for _, e := range pub.events {
		if e == "task.needs_human" {
			hasNeedsHuman = true
		}
	}
	if !hasNeedsHuman {
		t.Errorf("expected task.needs_human event; got %v", pub.events)
	}
}

func TestPool_Submit_DoesNotBlockWhenFull(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)

	// Pool with 0 workers — queue will fill
	pool := agent.NewPool(0, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	provider := &mockProvider{result: agent.Result{Status: "completed"}}
	job := buildJob(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), provider)

	// Flood the queue; Submit must never block
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			pool.Submit(job)
		}
		close(done)
	}()

	select {
	case <-done:
		// expected — all submits returned quickly
	case <-time.After(2 * time.Second):
		t.Error("Submit blocked when pool queue was full")
	}
}
