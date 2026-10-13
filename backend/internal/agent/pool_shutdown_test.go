package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// delayProvider finishes successfully after d (or fails if cancelled first),
// reporting a session id so resume can be checked.
type delayProvider struct{ d time.Duration }

func (p delayProvider) Run(ctx context.Context, _ agent.RunInput, _ chan<- agent.LogEntry) (agent.Result, error) {
	select {
	case <-time.After(p.d):
		return agent.Result{Status: "completed", SessionID: "sess-1"}, nil
	case <-ctx.Done():
		return agent.Result{Status: "failed", SessionID: "sess-1"}, ctx.Err()
	}
}

func shutdownFixture(t *testing.T) (*agent.Pool, *gen.Queries, string, string, string, string) {
	t.Helper()
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)
	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)
	if err := q.SetTaskActiveRun(context.Background(), gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID, ActiveAgentRunID: &runID, ID: taskID,
	}); err != nil {
		t.Fatalf("set active run: %v", err)
	}
	return pool, q, taskID, agCfgID, runID, wfs[0].ID
}

func TestPool_Shutdown_DrainLetsShortRunFinish(t *testing.T) {
	pool, q, taskID, agCfgID, runID, wfID := shutdownFixture(t)
	startPool(t, pool)
	pool.Submit(buildJob(runID, taskID, agCfgID, wfID, t.TempDir(), delayProvider{d: 300 * time.Millisecond}))
	waitForStatus(t, q, runID, "running")

	pool.Shutdown(10 * time.Second)

	run, err := q.GetAgentRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status == "interrupted" || run.Status == "running" {
		t.Fatalf("short run should finish inside the drain window, got %q", run.Status)
	}
	if pool.Submit(buildJob("x", taskID, agCfgID, wfID, t.TempDir(), delayProvider{})) {
		t.Error("Submit must refuse jobs once draining")
	}
}

func TestPool_Shutdown_WindowExpiredInterruptsAndReleasesLock(t *testing.T) {
	pool, q, taskID, agCfgID, runID, wfID := shutdownFixture(t)
	startPool(t, pool)
	// Pre-existing counters must survive an interrupt untouched.
	if _, err := q.IncrementTaskFailureCount(context.Background(), taskID); err != nil {
		t.Fatal(err)
	}
	pool.Submit(buildJob(runID, taskID, agCfgID, wfID, t.TempDir(), blockingProvider{}))
	waitForStatus(t, q, runID, "running")

	pool.Shutdown(100 * time.Millisecond)

	run, err := q.GetAgentRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "interrupted" {
		t.Fatalf("status = %q, want interrupted", run.Status)
	}
	task, _ := q.GetTask(context.Background(), taskID)
	if task.ActiveAgentRunID != nil {
		t.Errorf("lock not released: %q", *task.ActiveAgentRunID)
	}
	if task.Paused != 0 {
		t.Error("interrupted task must not be paused")
	}
	if task.ConsecutiveFailureCount != 1 || task.TransientRetryCount != 0 {
		t.Errorf("counters changed: failures=%d transient=%d", task.ConsecutiveFailureCount, task.TransientRetryCount)
	}
}

func TestPool_Shutdown_PersistsSessionForResume(t *testing.T) {
	pool, q, taskID, agCfgID, runID, wfID := shutdownFixture(t)
	startPool(t, pool)
	pool.Submit(buildJob(runID, taskID, agCfgID, wfID, t.TempDir(), sessionBlockingProvider{}))
	waitForStatus(t, q, runID, "running")
	pool.Shutdown(50 * time.Millisecond)

	sid, err := q.GetLatestTaskSession(context.Background(), gen.GetLatestTaskSessionParams{TaskID: taskID, AgentConfigID: &agCfgID})
	if err != nil || sid != "sess-1" {
		t.Fatalf("session = %q err=%v, want sess-1 stored for resume", sid, err)
	}
}

type sessionBlockingProvider struct{}

func (sessionBlockingProvider) Run(ctx context.Context, _ agent.RunInput, _ chan<- agent.LogEntry) (agent.Result, error) {
	<-ctx.Done()
	return agent.Result{Status: "failed", SessionID: "sess-1"}, ctx.Err()
}

func TestPool_RootContextCancelIsInterruptedNotFailed(t *testing.T) {
	pool, q, taskID, agCfgID, runID, wfID := shutdownFixture(t)
	_, stop := startPool(t, pool)
	pool.Submit(buildJob(runID, taskID, agCfgID, wfID, t.TempDir(), blockingProvider{}))
	waitForStatus(t, q, runID, "running")
	stop()
	run, _ := q.GetAgentRun(context.Background(), runID)
	if run.Status != "interrupted" {
		t.Fatalf("status = %q, want interrupted", run.Status)
	}
	task, _ := q.GetTask(context.Background(), taskID)
	if task.ActiveAgentRunID != nil || task.ConsecutiveFailureCount != 0 {
		t.Errorf("lock/counter wrong: %v %d", task.ActiveAgentRunID, task.ConsecutiveFailureCount)
	}
}
