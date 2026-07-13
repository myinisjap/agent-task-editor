package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

	beforeCompleted := testutil.ToFloat64(metrics.RunTerminalTotal.WithLabelValues("completed"))

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

	// ate_run_terminal_total{status="completed"} should have incremented by one.
	afterCompleted := testutil.ToFloat64(metrics.RunTerminalTotal.WithLabelValues("completed"))
	if afterCompleted-beforeCompleted != 1 {
		t.Errorf("expected RunTerminalTotal{completed} to increment by 1, got delta %v", afterCompleted-beforeCompleted)
	}
}

// TestPool_ResolvedComments_MarkedResolved verifies that when a completed run
// carries ResolvedComments (from the MCP resolve_comment tool), the pool marks
// the matching open review comments resolved with the run ID and note, and
// leaves unknown IDs alone.
func TestPool_ResolvedComments_MarkedResolved(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	commentID := uuid.NewString()
	_, err := q.CreateTaskReviewComment(context.Background(), gen.CreateTaskReviewCommentParams{
		ID:        commentID,
		TaskID:    taskID,
		FilePath:  "main.go",
		Side:      "new",
		StartLine: 1,
		EndLine:   2,
		Body:      "fix this",
	})
	if err != nil {
		t.Fatalf("create review comment: %v", err)
	}

	provider := &mockProvider{result: agent.Result{
		Status:  "completed",
		Outcome: "success",
		ResolvedComments: []agent.ResolvedComment{
			{ID: commentID, Note: "used the helper"},
			{ID: "not-a-real-comment", Note: "ignored"},
		},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	pool.Submit(buildJob(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), provider))

	waitForLabel(t, q, taskID, "review-plan")
	cancel()

	c, err := q.GetTaskReviewComment(context.Background(), gen.GetTaskReviewCommentParams{ID: commentID, TaskID: taskID})
	if err != nil {
		t.Fatalf("get review comment: %v", err)
	}
	if c.Status != "resolved" {
		t.Errorf("expected comment resolved, got %q", c.Status)
	}
	if c.ResolutionNote == nil || *c.ResolutionNote != "used the helper" {
		t.Errorf("expected resolution note preserved, got %v", c.ResolutionNote)
	}
	if c.ResolvedByRunID == nil || *c.ResolvedByRunID != runID {
		t.Errorf("expected resolved_by_run_id %q, got %v", runID, c.ResolvedByRunID)
	}
	if !pub.hasEvent("task.review_comments_changed") {
		t.Errorf("expected task.review_comments_changed event")
	}
}

// TestPool_ResolvedComments_IgnoredOnFailure verifies that a failed run's
// claimed resolutions are NOT applied — the fixes never landed on the branch.
func TestPool_ResolvedComments_IgnoredOnFailure(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	commentID := uuid.NewString()
	_, err := q.CreateTaskReviewComment(context.Background(), gen.CreateTaskReviewCommentParams{
		ID:        commentID,
		TaskID:    taskID,
		FilePath:  "main.go",
		Side:      "new",
		StartLine: 1,
		EndLine:   1,
		Body:      "fix this",
	})
	if err != nil {
		t.Fatalf("create review comment: %v", err)
	}

	provider := &mockProvider{result: agent.Result{
		Status:           "failed",
		ResolvedComments: []agent.ResolvedComment{{ID: commentID, Note: "claimed fix"}},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	pool.Submit(buildJob(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), provider))

	waitForStatus(t, q, runID, "failed")
	cancel()

	c, err := q.GetTaskReviewComment(context.Background(), gen.GetTaskReviewCommentParams{ID: commentID, TaskID: taskID})
	if err != nil {
		t.Fatalf("get review comment: %v", err)
	}
	if c.Status != "open" {
		t.Errorf("expected comment to stay open after failed run, got %q", c.Status)
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

	beforeFailed := testutil.ToFloat64(metrics.RunTerminalTotal.WithLabelValues("failed"))
	beforeGenuine := testutil.ToFloat64(metrics.RunClassificationTotal.WithLabelValues(string(agent.ClassGenuine)))

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	pool.Submit(buildJob(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), provider))

	waitForStatus(t, q, runID, "failed")
	cancel()

	run, _ := q.GetAgentRun(context.Background(), runID)
	if run.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", run.Status)
	}

	afterFailed := testutil.ToFloat64(metrics.RunTerminalTotal.WithLabelValues("failed"))
	if afterFailed-beforeFailed != 1 {
		t.Errorf("expected RunTerminalTotal{failed} to increment by 1, got delta %v", afterFailed-beforeFailed)
	}
	afterGenuine := testutil.ToFloat64(metrics.RunClassificationTotal.WithLabelValues(string(agent.ClassGenuine)))
	if afterGenuine-beforeGenuine != 1 {
		t.Errorf("expected RunClassificationTotal{genuine} to increment by 1, got delta %v", afterGenuine-beforeGenuine)
	}
}

// insertFailureHistory records n prior agent-triggered (from→to) failure legs
// plus their (to→from) return legs, so failureLoopExceeded sees a task already
// bouncing along the same rework edge. No human rows and no other exit from
// `from` are inserted, so the walk never breaks early — the count is exactly n
// regardless of same-second timestamp ordering.
func insertFailureHistory(t *testing.T, q *gen.Queries, taskID, from, to string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		f := from
		if err := q.CreateTaskLabelHistory(ctx, gen.CreateTaskLabelHistoryParams{
			ID: uuid.NewString(), TaskID: taskID, FromLabel: &f, ToLabel: to, Trigger: "agent",
		}); err != nil {
			t.Fatalf("insert failure history: %v", err)
		}
		back := to
		if err := q.CreateTaskLabelHistory(ctx, gen.CreateTaskLabelHistoryParams{
			ID: uuid.NewString(), TaskID: taskID, FromLabel: &back, ToLabel: from, Trigger: "agent",
		}); err != nil {
			t.Fatalf("insert return history: %v", err)
		}
	}
}

// buildJobAtLabel is buildJob with the task's current label overridden (the pool
// resolves outcomes from job.Input.Task.Label).
func buildJobAtLabel(runID, taskID, agCfgID, wfID, repoPath, label string, provider agent.Provider) agent.Job {
	job := buildJob(runID, taskID, agCfgID, wfID, repoPath, provider)
	job.Input.Task.Label = label
	return job
}

// TestPool_FailureLoop_RoutesFeedbackAndTransitions verifies that a completed
// run with a "failure" outcome (below the loop threshold) routes the agent's
// summary onto the run as feedback for the next Worker AND still fires the
// rework transition (agent-review → work).
func TestPool_FailureLoop_RoutesFeedbackAndTransitions(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)
	if _, err := db.SQL().Exec("UPDATE tasks SET label='agent-review' WHERE id=?", taskID); err != nil {
		t.Fatalf("move task label: %v", err)
	}
	// Two prior loops — below failureLoopThreshold, so this run should transition.
	insertFailureHistory(t, q, taskID, "agent-review", "work", 2)

	finding := "target_label is not validated against workflow labels (medium)"
	provider := &mockProvider{result: agent.Result{Status: "completed", Outcome: "failure", Message: &finding}}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)
	pool.Submit(buildJobAtLabel(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), "agent-review", provider))

	waitForLabel(t, q, taskID, "work")
	cancel()

	run, _ := q.GetAgentRun(context.Background(), runID)
	if run.Feedback == nil || *run.Feedback != finding {
		t.Errorf("expected run feedback %q, got %v", finding, run.Feedback)
	}
}

// TestPool_FailureLoop_EscalatesAfterThreshold verifies that once the same
// agent-review → work failure path has already fired failureLoopThreshold times,
// the pool stops looping: it parks the run in waiting_human, leaves the task on
// agent-review (does NOT transition), re-locks the task, and asks for a human.
func TestPool_FailureLoop_EscalatesAfterThreshold(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)
	if _, err := db.SQL().Exec("UPDATE tasks SET label='agent-review' WHERE id=?", taskID); err != nil {
		t.Fatalf("move task label: %v", err)
	}
	// Three prior loops == threshold, so this run must escalate instead of looping.
	insertFailureHistory(t, q, taskID, "agent-review", "work", 3)

	// Model the dispatcher having locked the task on this run (the pool unit-test
	// harness submits jobs directly, bypassing the dispatcher that normally sets
	// this). The escalation must LEAVE this lock in place.
	if err := q.SetTaskActiveRun(context.Background(), gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID, ActiveAgentRunID: &runID, ID: taskID,
	}); err != nil {
		t.Fatalf("lock task on run: %v", err)
	}

	finding := "same medium issue, again"
	provider := &mockProvider{result: agent.Result{Status: "completed", Outcome: "failure", Message: &finding}}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)
	pool.Submit(buildJobAtLabel(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), "agent-review", provider))

	waitForStatus(t, q, runID, "waiting_human")

	// Task must NOT have moved to work — the loop is broken, not continued.
	task, _ := q.GetTask(context.Background(), taskID)
	if task.Label != "agent-review" {
		t.Errorf("expected task to stay on 'agent-review', got %q", task.Label)
	}
	// Still locked on this run (the escalation never clears the lock) so the
	// dispatcher won't re-pick it until a human acts.
	if task.ActiveAgentRunID == nil || *task.ActiveAgentRunID != runID {
		t.Errorf("expected task still locked on run %s, got %v", runID, task.ActiveAgentRunID)
	}
	cancel()

	if !pub.hasEvent("task.needs_human") {
		t.Errorf("expected task.needs_human event on escalation")
	}
}

// blockingProvider waits until its context is cancelled, mimicking a CLI
// subprocess that only returns once killed by exec.CommandContext.
type blockingProvider struct{}

func (blockingProvider) Run(ctx context.Context, _ agent.RunInput, _ chan<- agent.LogEntry) (agent.Result, error) {
	<-ctx.Done()
	return agent.Result{Status: "failed"}, ctx.Err()
}

// TestPool_Cancel_MarksCancelledAndPauses verifies a human-requested cancel
// stops a running provider, records the run as "cancelled" (not failed), pauses
// the task, clears the active-run lock, and reports the run gone afterwards.
func TestPool_Cancel_MarksCancelledAndPauses(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	// Mark the task's active run so the clear-on-cancel is observable.
	if err := q.SetTaskActiveRun(context.Background(), gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID, ActiveAgentRunID: &runID, ID: taskID,
	}); err != nil {
		t.Fatalf("set active run: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pool.Start(ctx)

	pool.Submit(buildJob(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), blockingProvider{}))

	// Once the run is 'running' it is registered in the cancel registry.
	waitForStatus(t, q, runID, "running")
	if !pool.Cancel(runID) {
		t.Fatal("expected Cancel to find the active run")
	}

	waitForStatus(t, q, runID, "cancelled")

	task, _ := q.GetTask(context.Background(), taskID)
	if task.Paused == 0 {
		t.Errorf("expected task to be paused after cancel")
	}
	if task.ActiveAgentRunID != nil {
		t.Errorf("expected active_agent_run_id cleared, got %q", *task.ActiveAgentRunID)
	}
	if !pub.hasEvent("task.agent_done") {
		t.Errorf("expected task.agent_done event")
	}
}

// TestPool_Saturated verifies Saturated() reports false while a single-worker
// pool is idle, true once its one slot is occupied by an in-flight run, and
// false again once that run finishes and the worker frees up.
func TestPool_Saturated(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	if pool.Saturated() {
		t.Fatal("expected an idle pool to not be saturated")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pool.Start(ctx)

	pool.Submit(buildJob(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), blockingProvider{}))

	waitForStatus(t, q, runID, "running")
	if !pool.Saturated() {
		t.Error("expected pool to be saturated while its only worker is busy")
	}

	if !pool.Cancel(runID) {
		t.Fatal("expected Cancel to find the active run")
	}
	waitForStatus(t, q, runID, "cancelled")

	// Give the worker goroutine a moment to unregister the finished run.
	deadline := time.Now().Add(2 * time.Second)
	for pool.Saturated() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if pool.Saturated() {
		t.Error("expected pool to no longer be saturated once the run finished")
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

// TestListAgentPickupTasks_OrdersByPriorityThenCreatedAt verifies the
// dispatcher's pickup query returns eligible tasks ordered by priority
// descending (urgent, high, normal, low) and, within the same priority,
// oldest-created first.
func TestListAgentPickupTasks_OrdersByPriorityThenCreatedAt(t *testing.T) {
	db := openAgentTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()

	wfs, err := q.ListWorkflows(ctx)
	if err != nil || len(wfs) == 0 {
		t.Fatalf("list workflows: %v", err)
	}
	wfID := wfs[0].ID

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID:         repoID,
		Name:       "repo",
		Path:       t.TempDir(),
		WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// Create tasks in a deliberately mixed order: normal, low, urgent, high,
	// then a second normal-priority task created after the first normal one
	// to verify the created_at ASC tiebreak within a priority level.
	mkTask := func(title string, priority int64) string {
		id := uuid.NewString()
		if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
			ID:         id,
			Title:      title,
			WorkflowID: wfID,
			RepoID:     repoID,
			Label:      "plan",
			Priority:   priority,
		}); err != nil {
			t.Fatalf("create task %s: %v", title, err)
		}
		// Ensure distinct created_at ordering across the fixtures below;
		// SQLite's CURRENT_TIMESTAMP has second granularity.
		time.Sleep(1100 * time.Millisecond)
		return id
	}

	normal1 := mkTask("normal-1", 0)
	low := mkTask("low", -1)
	urgent := mkTask("urgent", 2)
	high := mkTask("high", 1)
	normal2 := mkTask("normal-2", 0)

	tasks, err := q.ListAgentPickupTasks(ctx)
	if err != nil {
		t.Fatalf("list pickup tasks: %v", err)
	}

	var gotOrder []string
	for _, tk := range tasks {
		switch tk.ID {
		case normal1, low, urgent, high, normal2:
			gotOrder = append(gotOrder, tk.ID)
		}
	}

	wantOrder := []string{urgent, high, normal1, normal2, low}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("expected %d fixture tasks in pickup order, got %d: %v", len(wantOrder), len(gotOrder), gotOrder)
	}
	for i, id := range wantOrder {
		if gotOrder[i] != id {
			t.Errorf("position %d: expected task %s, got %s (full order %v)", i, id, gotOrder[i], gotOrder)
		}
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

// initGitRepo creates a minimal git repo (with an initial commit) suitable
// for use as a job's RepoPath in safety-net-commit tests.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "init")
	return dir
}

// TestPool_SafetyNetCommit_HumanReadableMessage verifies that when the pool
// captures uncommitted agent work on run completion, the resulting commit
// message leads with the task title (not bare UUIDs) and demotes the task
// and run IDs to trailer lines.
func TestPool_SafetyNetCommit_HumanReadableMessage(t *testing.T) {
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)
	pool.GitName = "Test User"
	pool.GitEmail = "test@example.com"

	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)

	repoPath := initGitRepo(t)
	// Leave an uncommitted change in the repo for the pool's safety-net
	// commit to pick up.
	if err := os.WriteFile(filepath.Join(repoPath, "agent-work.txt"), []byte("work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("add", "-A")

	provider := &mockProvider{result: agent.Result{Status: "completed", Outcome: "success"}}
	job := buildJob(runID, taskID, agCfgID, wfs[0].ID, repoPath, provider)
	// buildJob's Task doesn't set RepoPath (only Input.RepoPath); the pool
	// keys its git lock off Task.RepoPath, so set both to the same repo.
	job.Input.Task.RepoPath = repoPath

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)

	pool.Submit(job)

	waitForLabel(t, q, taskID, "review-plan")
	cancel()

	out, err := exec.Command("git", "-C", repoPath, "log", "-1", "--format=%B").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	msg := string(out)

	if !strings.HasPrefix(msg, "Pool test task (safety-net commit)") {
		t.Errorf("expected message to start with task title, got:\n%s", msg)
	}
	if !strings.Contains(msg, "Task: "+taskID) {
		t.Errorf("expected Task trailer with task id %q, got:\n%s", taskID, msg)
	}
	if !strings.Contains(msg, "Agent-Run: "+runID) {
		t.Errorf("expected Agent-Run trailer with run id %q, got:\n%s", runID, msg)
	}
}
