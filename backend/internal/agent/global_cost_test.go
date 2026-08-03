package agent

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// globalCostFixture bundles a bare *Dispatcher (no running goroutines —
// refreshGlobalCostStatus/sweep are called synchronously by each test), its
// Queries handle, and the raw *sql.DB for backdating created_at directly
// (CreateAgentRun always stamps CURRENT_TIMESTAMP, and sqlc has no generated
// query for overriding it — this is test-only scaffolding). taskID is a
// single shared task lazily created by ensureTask and reused by every
// seedAgentRunWithCost call — agent_runs.task_id is FK-enforced, but none of
// these tests care about task identity, only the resulting agent_runs
// cost_usd/created_at values.
type globalCostFixture struct {
	d      *Dispatcher
	q      *gen.Queries
	db     *sql.DB
	taskID string
}

func newGlobalCostFixture(t *testing.T) *globalCostFixture {
	t.Helper()
	f, err := os.CreateTemp("", "global-cost-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	sdb, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	q := gen.New(sdb.SQL())
	d := NewDispatcher(sdb.SQL(), &Pool{maxWorkers: 1}, nil, nil)
	return &globalCostFixture{d: d, q: q, db: sdb.SQL()}
}

// ensureTask lazily creates one workflow/repo/task the fixture reuses across
// every seedAgentRunWithCost call.
func (f *globalCostFixture) ensureTask(t *testing.T) string {
	t.Helper()
	if f.taskID != "" {
		return f.taskID
	}
	ctx := context.Background()
	wfID := uuid.NewString()
	if _, err := f.q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "wf-cost-seed", Description: "t"}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	repoID := uuid.NewString()
	if _, err := f.q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	taskID := uuid.NewString()
	if _, err := f.q.CreateTask(ctx, gen.CreateTaskParams{ID: taskID, Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "parked"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	f.taskID = taskID
	return taskID
}

// seedAgentRunWithCost inserts a bare agent_runs row against the fixture's
// shared task (see ensureTask) with created_at forced to the given time,
// standing in for a run that already recorded cost during that calendar
// day/month.
func (f *globalCostFixture) seedAgentRunWithCost(t *testing.T, cost float64, createdAt time.Time) {
	t.Helper()
	ctx := context.Background()
	runID := uuid.NewString()
	taskID := f.ensureTask(t)
	if _, err := f.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: taskID}); err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	if _, err := f.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status: "completed", CostUsd: cost, ID: runID,
	}); err != nil {
		t.Fatalf("complete agent run: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE agent_runs SET created_at = ? WHERE id = ?`, createdAt.Format("2006-01-02 15:04:05"), runID); err != nil {
		t.Fatalf("backdate agent run: %v", err)
	}
}

func TestDispatcher_GlobalCostStatus_NoConfigMeansUnlimited(t *testing.T) {
	f := newGlobalCostFixture(t)
	// Neither MaxDailyCostUSD nor MaxMonthlyCostUSD set.
	tripped := f.d.refreshGlobalCostStatus(context.Background())
	if tripped {
		t.Fatal("expected no global cap configured to never trip")
	}
	status := f.d.GlobalCostStatus()
	if status.Tripped {
		t.Errorf("expected Tripped=false with no cap configured, got %+v", status)
	}
}

func TestDispatcher_GlobalCostStatus_DailyCapTrips(t *testing.T) {
	f := newGlobalCostFixture(t)
	f.d.MaxDailyCostUSD = 10

	f.seedAgentRunWithCost(t, 12, time.Now().UTC())

	tripped := f.d.refreshGlobalCostStatus(context.Background())
	if !tripped {
		t.Fatal("expected daily spend over the cap to trip dispatch")
	}
	status := f.d.GlobalCostStatus()
	if !status.Tripped || status.TrippedReason != "daily" {
		t.Errorf("expected Tripped=true reason=daily, got %+v", status)
	}
	if status.DailySpentUSD != 12 {
		t.Errorf("expected DailySpentUSD=12, got %v", status.DailySpentUSD)
	}
}

func TestDispatcher_GlobalCostStatus_MonthlyCapTrips(t *testing.T) {
	f := newGlobalCostFixture(t)
	f.d.MaxMonthlyCostUSD = 100

	f.seedAgentRunWithCost(t, 150, time.Now().UTC())

	tripped := f.d.refreshGlobalCostStatus(context.Background())
	if !tripped {
		t.Fatal("expected monthly spend over the cap to trip dispatch")
	}
	status := f.d.GlobalCostStatus()
	if !status.Tripped || status.TrippedReason != "monthly" {
		t.Errorf("expected Tripped=true reason=monthly, got %+v", status)
	}
}

func TestDispatcher_GlobalCostStatus_UnderCapDoesNotTrip(t *testing.T) {
	f := newGlobalCostFixture(t)
	f.d.MaxDailyCostUSD = 10
	f.d.MaxMonthlyCostUSD = 100

	f.seedAgentRunWithCost(t, 5, time.Now().UTC())

	tripped := f.d.refreshGlobalCostStatus(context.Background())
	if tripped {
		t.Fatal("expected spend under both caps to not trip dispatch")
	}
	status := f.d.GlobalCostStatus()
	if status.Tripped {
		t.Errorf("expected Tripped=false, got %+v", status)
	}
}

// TestDispatcher_GlobalCostStatus_OnlyCountsCurrentPeriod verifies spend
// recorded on a prior calendar day/month doesn't count toward today's/this
// month's cap — SumCostForDay/SumCostForMonth are period-scoped by design.
func TestDispatcher_GlobalCostStatus_OnlyCountsCurrentPeriod(t *testing.T) {
	f := newGlobalCostFixture(t)
	f.d.MaxDailyCostUSD = 10

	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	f.seedAgentRunWithCost(t, 999, yesterday)

	tripped := f.d.refreshGlobalCostStatus(context.Background())
	if tripped {
		t.Fatal("expected yesterday's spend to not count toward today's cap")
	}
}

// TestDispatcher_GlobalCostStatus_OneShotAlert verifies the tripped-alert
// publish fires exactly once on the transition into the tripped state, not
// on every subsequent sweep while it stays tripped.
func TestDispatcher_GlobalCostStatus_OneShotAlert(t *testing.T) {
	f := newGlobalCostFixture(t)
	pub := &recordingPub{}
	f.d.Publisher = pub
	f.d.MaxDailyCostUSD = 10

	f.seedAgentRunWithCost(t, 12, time.Now().UTC())

	f.d.refreshGlobalCostStatus(context.Background())
	f.d.refreshGlobalCostStatus(context.Background())
	f.d.refreshGlobalCostStatus(context.Background())

	count := 0
	pub.mu.Lock()
	for _, e := range pub.events {
		if e == "system.cost_budget_tripped" {
			count++
		}
	}
	pub.mu.Unlock()
	if count != 1 {
		t.Errorf("expected system.cost_budget_tripped to publish exactly once, got %d", count)
	}
}

// TestDispatcher_Sweep_HaltsDispatchWhenGlobalCapTripped verifies a tripped
// global cap stops sweep() from reaching ListAgentPickupTasks/dispatch at
// all — the cheapest possible proof is that a task sitting on a
// pickup-eligible label is never touched (no active_agent_run_id set).
func TestDispatcher_Sweep_HaltsDispatchWhenGlobalCapTripped(t *testing.T) {
	f := newGlobalCostFixture(t)
	f.d.MaxDailyCostUSD = 1
	f.seedAgentRunWithCost(t, 5, time.Now().UTC())

	ctx := context.Background()
	q := f.q
	wfID := uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "wf-sweep-halt", Description: "t"}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wfID, Name: "ready", Color: "#000", SortOrder: 0,
	}); err != nil {
		t.Fatalf("create label: %v", err)
	}
	sp := func(s string) *string { return &s }
	if _, err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
		ID: uuid.NewString(), WorkflowID: wfID, FromLabel: "ready", ToLabel: "next", TriggerType: "agent", Path: sp("success"),
	}); err != nil {
		t.Fatalf("create transition: %v", err)
	}
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	pc, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{ID: uuid.NewString(), Name: "pc", Provider: "fake", Model: "none", Env: `{}`})
	if err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	if _, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "cfg", ProviderConfigID: pc.ID, Labels: `["ready"]`,
	}); err != nil {
		t.Fatalf("create agent config: %v", err)
	}
	taskID := uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: taskID, Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "ready"}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	f.d.sweep(ctx)

	task, err := q.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.ActiveAgentRunID != nil {
		t.Errorf("expected task to remain undispatched while the global cap is tripped, got active_agent_run_id=%q", *task.ActiveAgentRunID)
	}
	if !f.d.GlobalCostStatus().Tripped {
		t.Error("expected GlobalCostStatus().Tripped to be true after the sweep")
	}
}
