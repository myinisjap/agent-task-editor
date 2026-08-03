package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// blockReasonFixture is a minimal workflow (label "ready" with an
// agent-triggered success transition to "next") plus a repo, used to build
// tasks that ARE dispatch candidates so BlockReasonResolver has something to
// evaluate. Modeled on wipFixture in dispatcher_wip_test.go. agentIgnore/
// nextWipLimit/nextWipHard let individual tests opt into the SQL-gate/WIP
// states that must be baked in at label-creation time (there is no
// UpdateWorkflowLabel query — labels are recreated wholesale elsewhere).
type blockReasonFixture struct {
	q          *gen.Queries
	workflowID string
	repoID     string
}

type blockReasonFixtureOpts struct {
	agentIgnoreReady bool
	nextWipLimit     *int64
	nextWipHard      bool
}

func newBlockReasonFixture(t *testing.T) *blockReasonFixture {
	t.Helper()
	return newBlockReasonFixtureWithOpts(t, blockReasonFixtureOpts{})
}

func newBlockReasonFixtureWithOpts(t *testing.T, opts blockReasonFixtureOpts) *blockReasonFixture {
	t.Helper()
	ctx := context.Background()

	f, err := os.CreateTemp("", "blockreason-*.db")
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
	q := gen.New(db.SQL())

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: uuid.NewString(), Name: "br-test-" + uuid.NewString()})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	agentIgnore := int64(0)
	if opts.agentIgnoreReady {
		agentIgnore = 1
	}
	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wf.ID, Name: "ready", Color: "#000", SortOrder: 0,
		AgentIgnore: agentIgnore,
	}); err != nil {
		t.Fatalf("create label ready: %v", err)
	}
	nextHard := int64(0)
	if opts.nextWipHard {
		nextHard = 1
	}
	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wf.ID, Name: "next", Color: "#000", SortOrder: 1,
		WipLimit: opts.nextWipLimit, WipLimitHard: nextHard,
	}); err != nil {
		t.Fatalf("create label next: %v", err)
	}
	sp := func(s string) *string { return &s }
	if _, err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
		ID: uuid.NewString(), WorkflowID: wf.ID, FromLabel: "ready", ToLabel: "next", TriggerType: "agent", Path: sp("success"),
	}); err != nil {
		t.Fatalf("create transition: %v", err)
	}

	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: uuid.NewString(), Name: "repo", Path: "/tmp/br-repo-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	return &blockReasonFixture{q: q, workflowID: wf.ID, repoID: repo.ID}
}

func (f *blockReasonFixture) newTask(t *testing.T, label string) gen.Task {
	t.Helper()
	task, err := f.q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "t", Type: "task", Label: label,
		RepoID: f.repoID, WorkflowID: f.workflowID, Attachments: "[]",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// setRepoMaxConcurrentRuns updates the fixture's repo's concurrency limit.
func (f *blockReasonFixture) setRepoMaxConcurrentRuns(t *testing.T, limit int64) {
	t.Helper()
	ctx := context.Background()
	repo, err := f.q.GetRepo(ctx, f.repoID)
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if _, err := f.q.UpdateRepo(ctx, gen.UpdateRepoParams{
		Name: repo.Name, Path: repo.Path, RemoteUrl: repo.RemoteUrl, WorkflowID: repo.WorkflowID,
		IssueSyncLabel: repo.IssueSyncLabel, IssueSyncUpdatePolicy: repo.IssueSyncUpdatePolicy,
		IssueSyncGoneAction: repo.IssueSyncGoneAction, IssueSyncGoneLabel: repo.IssueSyncGoneLabel,
		IssueWritebackLabel: repo.IssueWritebackLabel, MaxConcurrentRuns: &limit, ID: repo.ID,
	}); err != nil {
		t.Fatalf("set repo max_concurrent_runs: %v", err)
	}
}

// createEnabledConfig creates a provider config + enabled agent config
// matching the given label, returning the agent config id.
func (f *blockReasonFixture) createEnabledConfig(t *testing.T, label string) string {
	t.Helper()
	ctx := context.Background()
	pc, err := f.q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{
		ID: uuid.NewString(), Name: "test-provider", Provider: "fake", Model: "none", Env: `{}`,
	})
	if err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	cfg, err := f.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "fake-agent", ProviderConfigID: pc.ID,
		Labels: `["` + label + `"]`, MaxRetries: 1, RetryBackoffSecs: 1,
	})
	if err != nil {
		t.Fatalf("create agent config: %v", err)
	}
	return cfg.ID
}

func resolveOne(t *testing.T, r *BlockReasonResolver, task gen.Task) *BlockReason {
	t.Helper()
	m, err := r.ResolveMany(context.Background(), []gen.Task{task})
	if err != nil {
		t.Fatalf("ResolveMany: %v", err)
	}
	return m[task.ID]
}

func TestBlockReasonResolver_NotACandidate(t *testing.T) {
	f := newBlockReasonFixture(t)
	r := NewBlockReasonResolver(f.q, nil, nil)

	t.Run("archived task has no reason", func(t *testing.T) {
		task := f.newTask(t, "ready")
		task, err := f.q.SetTaskArchived(context.Background(), gen.SetTaskArchivedParams{Archived: 1, ID: task.ID})
		if err != nil {
			t.Fatalf("archive: %v", err)
		}
		if reason := resolveOne(t, r, task); reason != nil {
			t.Fatalf("expected no block reason for archived task, got %+v", reason)
		}
	})

	t.Run("already-running task has no reason", func(t *testing.T) {
		task := f.newTask(t, "ready")
		runID := uuid.NewString()
		if _, err := f.q.CreateAgentRun(context.Background(), gen.CreateAgentRunParams{ID: runID, TaskID: task.ID}); err != nil {
			t.Fatalf("create run: %v", err)
		}
		if err := f.q.SetTaskActiveRun(context.Background(), gen.SetTaskActiveRunParams{
			CurrentAgentRunID: &runID, ActiveAgentRunID: &runID, ID: task.ID,
		}); err != nil {
			t.Fatalf("lock task: %v", err)
		}
		task, err := f.q.GetTask(context.Background(), task.ID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if reason := resolveOne(t, r, task); reason != nil {
			t.Fatalf("expected no block reason for already-running task, got %+v", reason)
		}
	})

	t.Run("non-agent-triggerable label has no reason", func(t *testing.T) {
		task := f.newTask(t, "next") // "next" has no outgoing agent transition
		if reason := resolveOne(t, r, task); reason != nil {
			t.Fatalf("expected no block reason for non-pickup label, got %+v", reason)
		}
	})
}

func TestBlockReasonResolver_Paused(t *testing.T) {
	f := newBlockReasonFixture(t)
	r := NewBlockReasonResolver(f.q, nil, nil)

	task := f.newTask(t, "ready")
	task, err := f.q.SetTaskPaused(context.Background(), gen.SetTaskPausedParams{Paused: 1, ID: task.ID})
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	reason := resolveOne(t, r, task)
	if reason == nil || reason.Code != BlockPaused {
		t.Fatalf("expected %q, got %+v", BlockPaused, reason)
	}
}

func TestBlockReasonResolver_AgentIgnore(t *testing.T) {
	f := newBlockReasonFixtureWithOpts(t, blockReasonFixtureOpts{agentIgnoreReady: true})
	r := NewBlockReasonResolver(f.q, nil, nil)
	task := f.newTask(t, "ready")
	reason := resolveOne(t, r, task)
	if reason == nil || reason.Code != BlockAgentIgnore {
		t.Fatalf("expected %q, got %+v", BlockAgentIgnore, reason)
	}
}

func TestBlockReasonResolver_Dependency(t *testing.T) {
	ctx := context.Background()
	f := newBlockReasonFixture(t)
	blocker := f.newTask(t, "ready")
	task := f.newTask(t, "ready")
	if err := f.q.CreateTaskDependency(ctx, gen.CreateTaskDependencyParams{
		TaskID: task.ID, DependsOnTaskID: blocker.ID,
	}); err != nil {
		t.Fatalf("create dependency: %v", err)
	}

	r := NewBlockReasonResolver(f.q, nil, nil)
	reason := resolveOne(t, r, task)
	if reason == nil || reason.Code != BlockDependency {
		t.Fatalf("expected %q, got %+v", BlockDependency, reason)
	}
	details, ok := reason.Detail.([]blockerDetail)
	if !ok || len(details) != 1 || details[0].TaskID != blocker.ID {
		t.Fatalf("expected detail to name blocker %q, got %+v", blocker.ID, reason.Detail)
	}
}

func TestBlockReasonResolver_RetryBackoff(t *testing.T) {
	f := newBlockReasonFixture(t)
	task := f.newTask(t, "ready")
	future := time.Now().Add(time.Hour)
	task, err := f.q.SetTaskTransientRetry(context.Background(), gen.SetTaskTransientRetryParams{
		TransientRetryCount: 2, NextRetryAt: &future, ID: task.ID,
	})
	if err != nil {
		t.Fatalf("set retry: %v", err)
	}

	r := NewBlockReasonResolver(f.q, nil, nil)
	reason := resolveOne(t, r, task)
	if reason == nil || reason.Code != BlockRetryBackoff {
		t.Fatalf("expected %q, got %+v", BlockRetryBackoff, reason)
	}
	if reason.ClearsAt == nil || !reason.ClearsAt.Equal(future) {
		t.Fatalf("expected ClearsAt %v, got %v", future, reason.ClearsAt)
	}
}

func TestBlockReasonResolver_NoConfig(t *testing.T) {
	f := newBlockReasonFixture(t)
	task := f.newTask(t, "ready")

	r := NewBlockReasonResolver(f.q, nil, nil)
	reason := resolveOne(t, r, task)
	if reason == nil || reason.Code != BlockNoConfig {
		t.Fatalf("expected %q, got %+v", BlockNoConfig, reason)
	}
}

func TestBlockReasonResolver_RepoConcurrency(t *testing.T) {
	ctx := context.Background()
	f := newBlockReasonFixture(t)
	f.createEnabledConfig(t, "ready")
	f.setRepoMaxConcurrentRuns(t, 1)

	// Occupy the repo's only slot with a different, already-running task.
	running := f.newTask(t, "ready")
	runID := uuid.NewString()
	if _, err := f.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: running.ID}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := f.q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID, ActiveAgentRunID: &runID, ID: running.ID,
	}); err != nil {
		t.Fatalf("lock running task: %v", err)
	}

	task := f.newTask(t, "ready")
	r := NewBlockReasonResolver(f.q, nil, nil)
	reason := resolveOne(t, r, task)
	if reason == nil || reason.Code != BlockRepoConcurrency {
		t.Fatalf("expected %q, got %+v", BlockRepoConcurrency, reason)
	}
}

func TestBlockReasonResolver_RateLimited(t *testing.T) {
	f := newBlockReasonFixture(t)
	cfgID := f.createEnabledConfig(t, "ready")
	task := f.newTask(t, "ready")

	rl := NewRateLimitRegistry()
	until := time.Now().Add(30 * time.Minute)
	rl.Block(cfgID, until)

	r := NewBlockReasonResolver(f.q, nil, rl)
	reason := resolveOne(t, r, task)
	if reason == nil || reason.Code != BlockRateLimited {
		t.Fatalf("expected %q, got %+v", BlockRateLimited, reason)
	}
	if reason.ClearsAt == nil || !reason.ClearsAt.Equal(until) {
		t.Fatalf("expected ClearsAt %v, got %v", until, reason.ClearsAt)
	}
}

func TestBlockReasonResolver_CostBudgetExhausted(t *testing.T) {
	ctx := context.Background()
	f := newBlockReasonFixture(t)
	cfgID := f.createEnabledConfig(t, "ready")
	task := f.newTask(t, "ready")

	if _, err := f.q.UpdateTask(ctx, gen.UpdateTaskParams{
		Title: task.Title, Description: task.Description, Type: task.Type, RepoID: task.RepoID,
		MaxCostUsd: 1.0, ID: task.ID,
	}); err != nil {
		t.Fatalf("set task budget: %v", err)
	}

	runID := uuid.NewString()
	if _, err := f.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: task.ID, AgentConfigID: &cfgID}); err != nil {
		t.Fatalf("create prior run: %v", err)
	}
	if _, err := f.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status: "completed", CostUsd: 2.0, ID: runID,
	}); err != nil {
		t.Fatalf("complete prior run: %v", err)
	}

	task, err := f.q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	r := NewBlockReasonResolver(f.q, nil, nil)
	reason := resolveOne(t, r, task)
	if reason == nil || reason.Code != BlockCostBudget {
		t.Fatalf("expected %q, got %+v", BlockCostBudget, reason)
	}

	// Read-only guarantee: no phantom run/lock was created by the resolver.
	fresh, err := f.q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task after resolve: %v", err)
	}
	if fresh.ActiveAgentRunID != nil {
		t.Fatalf("resolver must not write: expected active_agent_run_id to stay nil, got %v", fresh.ActiveAgentRunID)
	}
	runs, err := f.q.ListAgentRuns(ctx, task.ID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("resolver must not write: expected exactly the 1 seeded run, got %d", len(runs))
	}
}

func TestBlockReasonResolver_WIPLimit(t *testing.T) {
	limit := int64(1)
	f := newBlockReasonFixtureWithOpts(t, blockReasonFixtureOpts{nextWipLimit: &limit, nextWipHard: true})
	f.createEnabledConfig(t, "ready")
	f.newTask(t, "next") // fills the WIP limit of "next"

	task := f.newTask(t, "ready")
	r := NewBlockReasonResolver(f.q, nil, nil)
	reason := resolveOne(t, r, task)
	if reason == nil || reason.Code != BlockWIPLimit {
		t.Fatalf("expected %q, got %+v", BlockWIPLimit, reason)
	}
}

// TestBlockReasonResolver_Ordering pins the resolution order: a task that is
// both paused and rate-limited must report "paused" (the first gate), not
// "rate_limited" — clearing paused might still leave it rate-limited, but
// reporting only one reason at a time keeps the UI honest about what to fix
// first. See BlockReasonResolver.resolveOne's doc comment for the full order.
func TestBlockReasonResolver_Ordering(t *testing.T) {
	f := newBlockReasonFixture(t)
	cfgID := f.createEnabledConfig(t, "ready")
	task := f.newTask(t, "ready")
	task, err := f.q.SetTaskPaused(context.Background(), gen.SetTaskPausedParams{Paused: 1, ID: task.ID})
	if err != nil {
		t.Fatalf("pause: %v", err)
	}

	rl := NewRateLimitRegistry()
	rl.Block(cfgID, time.Now().Add(time.Hour))

	r := NewBlockReasonResolver(f.q, nil, rl)
	reason := resolveOne(t, r, task)
	if reason == nil || reason.Code != BlockPaused {
		t.Fatalf("expected paused to take priority over rate_limited, got %+v", reason)
	}
}

func TestBlockReasonResolver_NoResolverIsNilSafe(t *testing.T) {
	// blockReasonMap in the handlers package guards on a nil resolver; this
	// just documents that ResolveMany itself is safe to call with an empty
	// task slice (used when no tasks are on the page).
	f := newBlockReasonFixture(t)
	r := NewBlockReasonResolver(f.q, nil, nil)
	m, err := r.ResolveMany(context.Background(), nil)
	if err != nil {
		t.Fatalf("ResolveMany with no tasks: %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil map for empty input, got %+v", m)
	}
}
