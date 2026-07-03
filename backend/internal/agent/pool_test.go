package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
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
	mu     sync.Mutex
	events []string
}

func (p *testPub) Publish(eventType string, _ map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, eventType)
}

func (p *testPub) hasEvent(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.events {
		if e == name {
			return true
		}
	}
	return false
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
		Label:      "plan",
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
		Labels:   `["plan"]`,
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
	return buildJobWithRetry(runID, taskID, agCfgID, wfID, repoPath, provider, 0, 0)
}

// buildJobWithRetry is like buildJob but lets tests configure the agent
// config's retry policy fields carried on the job's AgentConfig.
func buildJobWithRetry(runID, taskID, agCfgID, wfID, repoPath string, provider agent.Provider, maxRetries, retryBackoffSecs int64) agent.Job {
	return agent.Job{
		RunID:    runID,
		Provider: provider,
		Input: agent.RunInput{
			RunID: runID,
			Task:  agent.Task{ID: taskID, Title: "Pool test task", Label: "plan", WorkflowID: wfID},
			AgentConfig: agent.AgentConfig{
				ID:               agCfgID,
				Name:             "mock-agent",
				Provider:         "mock",
				MaxRetries:       maxRetries,
				RetryBackoffSecs: retryBackoffSecs,
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

// waitForLabel polls until the task has the expected label or times out.
// The run row flips to "completed" before the pool resolves the outcome and
// transitions the label, so callers must wait on the label itself rather
// than the run status to avoid racing the pool's post-completion work.
func waitForLabel(t *testing.T, q *gen.Queries, taskID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, err := q.GetTask(context.Background(), taskID)
		if err == nil && task.Label == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	task, _ := q.GetTask(context.Background(), taskID)
	t.Errorf("timed out waiting for task %s to reach label %q; current: %q", taskID, want, task.Label)
}

// waitForTaskRetryCount polls until the task's transient_retry_count matches want.
func waitForTaskRetryCount(t *testing.T, q *gen.Queries, taskID string, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, err := q.GetTask(context.Background(), taskID)
		if err == nil && task.TransientRetryCount == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	task, _ := q.GetTask(context.Background(), taskID)
	t.Errorf("timed out waiting for task %s transient_retry_count to reach %d; current: %d", taskID, want, task.TransientRetryCount)
}

// TestReDispatch_PicksUpConfigChanges verifies a re-run resolves its agent
// config fresh from the DB each sweep — disabling the prior config and enabling
// another for the same label makes the next dispatch switch configs, rather than
// reusing whatever the previous run recorded.
func TestReDispatch_PicksUpConfigChanges(t *testing.T) {
	db := openAgentTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()

	// Config A: enabled, owns "plan". This is what the first run uses.
	cfgA := uuid.NewString()
	if _, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: cfgA, Name: "planner-A", Provider: "mock", Model: "none", Labels: `["plan"]`, Env: `{}`,
	}); err != nil {
		t.Fatalf("create config A: %v", err)
	}
	// Config B: also owns "plan", but starts disabled.
	cfgB := uuid.NewString()
	if _, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: cfgB, Name: "planner-B", Provider: "mock", Model: "none", Labels: `["plan"]`, Env: `{}`,
	}); err != nil {
		t.Fatalf("create config B: %v", err)
	}
	disable(t, q, cfgB)

	// First dispatch resolves config A (the only enabled "plan" config).
	first := dispatchPickConfig(t, q, "plan")
	if first == nil || first.ID != cfgA {
		t.Fatalf("first dispatch: want config A, got %v", first)
	}

	// Operator flips configs between runs: disable A, enable B.
	disable(t, q, cfgA)
	enable(t, q, cfgB)

	// Re-dispatch must switch to B, not reuse A from the prior run.
	second := dispatchPickConfig(t, q, "plan")
	if second == nil || second.ID != cfgB {
		t.Fatalf("re-dispatch: want config B, got %v", second)
	}
}

// dispatchPickConfig mirrors how the dispatcher selects a config each sweep:
// list enabled configs fresh, then match by the task's current label.
func dispatchPickConfig(t *testing.T, q *gen.Queries, label string) *gen.AgentConfig {
	t.Helper()
	configs, err := q.ListAgentConfigs(context.Background())
	if err != nil {
		t.Fatalf("list configs: %v", err)
	}
	for i := range configs {
		var labels []string
		_ = json.Unmarshal([]byte(configs[i].Labels), &labels)
		for _, l := range labels {
			if l == label {
				return &configs[i]
			}
		}
	}
	return nil
}

func disable(t *testing.T, q *gen.Queries, id string) { setEnabled(t, q, id, false) }
func enable(t *testing.T, q *gen.Queries, id string)  { setEnabled(t, q, id, true) }

func setEnabled(t *testing.T, q *gen.Queries, id string, enabled bool) {
	t.Helper()
	cur, err := q.GetAgentConfig(context.Background(), id)
	if err != nil {
		t.Fatalf("get config %s: %v", id, err)
	}
	var e int64
	if enabled {
		e = 1
	}
	if _, err := q.UpdateAgentConfig(context.Background(), gen.UpdateAgentConfigParams{
		ID: id, Name: cur.Name, Provider: cur.Provider, Model: cur.Model,
		SystemPrompt: cur.SystemPrompt, Labels: cur.Labels, Env: cur.Env,
		MaxTokens: cur.MaxTokens, TimeoutSecs: cur.TimeoutSecs, Enabled: e,
	}); err != nil {
		t.Fatalf("update config %s: %v", id, err)
	}
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

	waitForLabel(t, q, taskID, "review-plan")
	cancel()

	// Task label should have been transitioned
	task, _ := q.GetTask(context.Background(), taskID)
	if task.Label != "review-plan" {
		t.Errorf("expected label 'review-plan', got %q", task.Label)
	}

	// Events: agent_started + agent_done (label_changed also fired by engine)
	if !pub.hasEvent("task.agent_started") {
		t.Errorf("expected task.agent_started event")
	}
	if !pub.hasEvent("task.agent_done") {
		t.Errorf("expected task.agent_done event")
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

	if !pub.hasEvent("task.needs_human") {
		t.Errorf("expected task.needs_human event")
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

func TestPool_TransientFailure_RetriesUnderCap(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	provider := &mockProvider{err: &agent.ErrTransient{Cause: context.DeadlineExceeded}}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	// max_retries=3, base backoff 30s — well within the budget on the first failure.
	pool.Submit(buildJobWithRetry(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), provider, 3, 30))

	waitForStatus(t, q, runID, "failed")
	waitForTaskRetryCount(t, q, taskID, 1)
	cancel()

	task, err := q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.TransientRetryCount != 1 {
		t.Errorf("expected transient_retry_count=1, got %d", task.TransientRetryCount)
	}
	if task.NextRetryAt == nil {
		t.Error("expected next_retry_at to be set")
	} else if !task.NextRetryAt.After(time.Now()) {
		t.Errorf("expected next_retry_at in the future, got %v", *task.NextRetryAt)
	}
	if task.ActiveAgentRunID != nil {
		t.Error("expected active_agent_run_id cleared so the task can be re-picked once eligible")
	}
	run, _ := q.GetAgentRun(context.Background(), runID)
	if run.Status != "failed" {
		t.Errorf("expected run status 'failed', got %q", run.Status)
	}
}

func TestPool_TransientFailure_EscalatesAfterMaxRetries(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	// Pre-seed the task at the retry cap (max_retries=1) so this run should escalate.
	if _, err := q.SetTaskTransientRetry(context.Background(), gen.SetTaskTransientRetryParams{
		TransientRetryCount: 1,
		ID:                  taskID,
	}); err != nil {
		t.Fatalf("seed retry count: %v", err)
	}
	// Mirror a real dispatch: the dispatcher sets active_agent_run_id before
	// handing the job to the pool. seedJobFixtures doesn't do this, so set it
	// explicitly to verify handleTransientFailure leaves it locked on escalation.
	if err := q.SetTaskActiveRun(context.Background(), gen.SetTaskActiveRunParams{
		ActiveAgentRunID: &runID,
		ID:               taskID,
	}); err != nil {
		t.Fatalf("seed active run: %v", err)
	}

	provider := &mockProvider{err: &agent.ErrTransient{Cause: context.DeadlineExceeded}}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	pool.Submit(buildJobWithRetry(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), provider, 1, 1))

	waitForStatus(t, q, runID, "waiting_human")
	cancel()

	if !pub.hasEvent("task.needs_human") {
		t.Error("expected task.needs_human event on retry-budget exhaustion")
	}

	task, err := q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	// The counter resets on escalation so a human-triggered re-dispatch starts fresh.
	if task.TransientRetryCount != 0 {
		t.Errorf("expected transient_retry_count reset to 0 after escalation, got %d", task.TransientRetryCount)
	}
	if task.ActiveAgentRunID == nil {
		t.Error("expected active_agent_run_id to remain set (locked) while waiting_human")
	}
}

func TestPool_GenuineFailure_DoesNotConsumeRetryBudget(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	// A plain Result{Status:"failed"} with no error — the agent ran and decided
	// the task itself failed. This must not touch the retry counter.
	provider := &mockProvider{result: agent.Result{Status: "failed"}}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	pool.Submit(buildJobWithRetry(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), provider, 3, 30))

	waitForStatus(t, q, runID, "failed")
	cancel()

	task, err := q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.TransientRetryCount != 0 {
		t.Errorf("expected transient_retry_count to stay 0 for a genuine failure, got %d", task.TransientRetryCount)
	}
	if task.NextRetryAt != nil {
		t.Errorf("expected next_retry_at to stay nil for a genuine failure, got %v", *task.NextRetryAt)
	}
}

func TestPool_Success_ResetsRetryCount(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	// Simulate prior transient failures having bumped the counter.
	if _, err := q.SetTaskTransientRetry(context.Background(), gen.SetTaskTransientRetryParams{
		TransientRetryCount: 2,
		ID:                  taskID,
	}); err != nil {
		t.Fatalf("seed retry count: %v", err)
	}

	provider := &mockProvider{result: agent.Result{Status: "completed"}}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	pool.Submit(buildJobWithRetry(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), provider, 3, 30))

	waitForStatus(t, q, runID, "completed")
	cancel()

	task, err := q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.TransientRetryCount != 0 {
		t.Errorf("expected transient_retry_count reset to 0 after a successful run, got %d", task.TransientRetryCount)
	}
}

// TestListAgentPickupTasks_ExcludesFutureNextRetryAt verifies the dispatcher's
// pickup query skips tasks that are in a backed-off transient-retry window
// (next_retry_at in the future), and picks them back up once that time has
// passed or been cleared.
func TestListAgentPickupTasks_ExcludesFutureNextRetryAt(t *testing.T) {
	db := openAgentTestDB(t)
	q := gen.New(db.SQL())

	wfs, err := q.ListWorkflows(context.Background())
	if err != nil || len(wfs) == 0 {
		t.Fatalf("list workflows: %v", err)
	}
	taskID, _, _ := seedJobFixtures(t, q, wfs[0].ID)

	// No active run and no next_retry_at — task should be pickup-eligible.
	tasks, err := q.ListAgentPickupTasks(context.Background())
	if err != nil {
		t.Fatalf("list pickup tasks: %v", err)
	}
	if !containsTaskID(tasks, taskID) {
		t.Fatalf("expected task %s to be pickup-eligible before any retry scheduling", taskID)
	}

	// Schedule a future retry — task should now be excluded.
	future := time.Now().Add(1 * time.Hour)
	if _, err := q.SetTaskTransientRetry(context.Background(), gen.SetTaskTransientRetryParams{
		TransientRetryCount: 1,
		NextRetryAt:         &future,
		ID:                  taskID,
	}); err != nil {
		t.Fatalf("set transient retry: %v", err)
	}
	tasks, err = q.ListAgentPickupTasks(context.Background())
	if err != nil {
		t.Fatalf("list pickup tasks: %v", err)
	}
	if containsTaskID(tasks, taskID) {
		t.Fatalf("expected task %s to be excluded while next_retry_at is in the future", taskID)
	}

	// A past next_retry_at should make it eligible again.
	past := time.Now().Add(-1 * time.Minute)
	if _, err := q.SetTaskTransientRetry(context.Background(), gen.SetTaskTransientRetryParams{
		TransientRetryCount: 1,
		NextRetryAt:         &past,
		ID:                  taskID,
	}); err != nil {
		t.Fatalf("set transient retry: %v", err)
	}
	tasks, err = q.ListAgentPickupTasks(context.Background())
	if err != nil {
		t.Fatalf("list pickup tasks: %v", err)
	}
	if !containsTaskID(tasks, taskID) {
		t.Fatalf("expected task %s to be pickup-eligible once next_retry_at has passed", taskID)
	}

	// Resetting clears next_retry_at entirely — also eligible.
	if _, err := q.ResetTaskTransientRetry(context.Background(), taskID); err != nil {
		t.Fatalf("reset transient retry: %v", err)
	}
	tasks, err = q.ListAgentPickupTasks(context.Background())
	if err != nil {
		t.Fatalf("list pickup tasks: %v", err)
	}
	if !containsTaskID(tasks, taskID) {
		t.Fatalf("expected task %s to be pickup-eligible after reset", taskID)
	}
}

func containsTaskID(tasks []gen.Task, taskID string) bool {
	for _, t := range tasks {
		if t.ID == taskID {
			return true
		}
	}
	return false
}
