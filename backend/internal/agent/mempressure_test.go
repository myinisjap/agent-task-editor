package agent

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func fakeClockTracker() (*memPressureTracker, *time.Time) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	return &memPressureTracker{now: func() time.Time { return now }}, &now
}

func TestMemPressure_CooldownGrowthAndCap(t *testing.T) {
	tr, now := fakeClockTracker()
	want := []time.Duration{60 * time.Second, 120 * time.Second, 240 * time.Second, 300 * time.Second, 300 * time.Second}
	for i, w := range want {
		cd, streak, _, _ := tr.recordCancel("t1")
		if cd != w || streak != i+1 {
			t.Fatalf("cancel %d: cooldown=%v streak=%d, want %v/%d", i+1, cd, streak, w, i+1)
		}
		*now = now.Add(memBurstWindow + time.Second) // keep the global burst out of the way
	}
}

func TestMemPressure_StreakResetsAfterGap(t *testing.T) {
	tr, now := fakeClockTracker()
	tr.recordCancel("t1")
	tr.recordCancel("t1")
	*now = now.Add(memStreakWindow + time.Second)
	cd, streak, _, _ := tr.recordCancel("t1")
	if cd != memCooldownBase || streak != 1 {
		t.Fatalf("expected reset to base, got %v streak %d", cd, streak)
	}
}

func TestMemPressure_BurstTripsGlobalPause(t *testing.T) {
	tr, now := fakeClockTracker()
	for i, id := range []string{"a", "b"} {
		if _, _, tripped, _ := tr.recordCancel(id); tripped {
			t.Fatalf("cancel %d must not trip", i+1)
		}
		*now = now.Add(10 * time.Second)
	}
	if _, active := tr.pausedUntilTime(); active {
		t.Fatal("pause must not be active before the threshold")
	}
	_, _, tripped, until := tr.recordCancel("c")
	if !tripped || !until.Equal(now.Add(memPauseDuration)) {
		t.Fatalf("expected trip with pause until now+%v, got tripped=%v until=%v", memPauseDuration, tripped, until)
	}
	if _, active := tr.pausedUntilTime(); !active {
		t.Fatal("expected pause active")
	}
	*now = now.Add(memPauseDuration + time.Second)
	if _, active := tr.pausedUntilTime(); active {
		t.Fatal("expected pause to expire")
	}
}

func TestMemPressure_SpreadCancelsNeverTrip(t *testing.T) {
	tr, now := fakeClockTracker()
	for i := 0; i < 10; i++ {
		if _, _, tripped, _ := tr.recordCancel("x"); tripped {
			t.Fatalf("cancel %d tripped despite wide spacing", i)
		}
		*now = now.Add(memBurstWindow)
	}
}

func TestMemoryPressurePause_NilPool(t *testing.T) {
	var p *Pool
	if _, active := p.MemoryPressurePause(); active {
		t.Fatal("nil pool must report inactive")
	}
}

func TestDispatcher_Sweep_HaltsDispatchWhenMemoryPaused(t *testing.T) {
	f := newGlobalCostFixture(t)
	f.d.pool.memPressure.pausedUntil = time.Now().Add(time.Minute)

	ctx := context.Background()
	q := f.q
	wfID := uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "wf-mem-pause", Description: "t"}); err != nil {
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
		t.Errorf("expected no dispatch during memory pause, got active_agent_run_id=%q", *task.ActiveAgentRunID)
	}
	if v := testutil.ToFloat64(metrics.DispatchMemoryPauseActive); v != 1 {
		t.Errorf("expected pause gauge 1, got %v", v)
	}
}
