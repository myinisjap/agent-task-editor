package agent

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestSelectRung(t *testing.T) {
	for _, c := range []struct{ failed, n, want int }{{0, 2, 0}, {1, 2, 1}, {2, 2, 2}, {5, 2, 2}, {3, 0, 0}, {-1, 2, 0}} {
		if got := selectRung(c.failed, c.n); got != c.want {
			t.Errorf("selectRung(%d,%d)=%d want %d", c.failed, c.n, got, c.want)
		}
	}
}

func hrow(from, to, trigger string) gen.TaskLabelHistory {
	h := gen.TaskLabelHistory{ToLabel: to, Trigger: trigger}
	if from != "" {
		h.FromLabel = &from
	}
	return h
}

func TestReworkBounces(t *testing.T) {
	fail := func(from, to string) bool { return from == "review" && to == "work" }
	if n := reworkBounces(nil, "work", fail); n != 0 {
		t.Errorf("empty = %d", n)
	}
	hist := []gen.TaskLabelHistory{
		hrow("", "work", "system"),
		hrow("work", "review", "agent"),
		hrow("review", "work", "agent"),
		hrow("work", "review", "agent"),
		hrow("review", "work", "agent"),
	}
	if n := reworkBounces(hist, "work", fail); n != 2 {
		t.Errorf("two bounces = %d", n)
	}
	// human row resets
	h2 := append([]gen.TaskLabelHistory{}, hist...)
	h2 = append(h2, hrow("work", "review", "human"))
	if n := reworkBounces(h2, "work", fail); n != 0 {
		t.Errorf("human reset = %d", n)
	}
	// fresh success-path arrival resets
	h3 := append([]gen.TaskLabelHistory{}, hist...)
	h3 = append(h3, hrow("work", "review", "agent"), hrow("triage", "work", "agent"))
	if n := reworkBounces(h3, "work", fail); n != 0 {
		t.Errorf("fresh arrival = %d", n)
	}
}

func TestRungProviderIDsAndEffort(t *testing.T) {
	cfg := &gen.AgentConfig{ProviderConfigID: "p", FallbackProviderConfigIds: `["f"]`, Effort: "low"}
	ladder := []LadderRung{{ProviderConfigID: "big", FallbackProviderConfigIDs: []string{"p", "bf"}}, {ProviderConfigID: "p", Effort: "max"}}
	eq := func(got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("got %v want %v", got, want)
			}
		}
	}
	eq(rungProviderIDs(cfg, ladder, 0), []string{"p", "f"})
	eq(rungProviderIDs(cfg, ladder, 1), []string{"big", "p", "bf", "f"})
	eq(rungProviderIDs(cfg, ladder, 2), []string{"p", "f"})
	if e := rungEffort(*cfg, ladder, 1); e != "low" {
		t.Errorf("inherit effort = %q", e)
	}
	if e := rungEffort(*cfg, ladder, 2); e != "max" {
		t.Errorf("rung effort = %q", e)
	}
	if e := rungEffort(*cfg, ladder, 0); e != "low" {
		t.Errorf("rung0 effort = %q", e)
	}
}

// seedLadderTask creates the repo, task (with the given breaker count) and a
// ladder agent config on "ready": rung 1 = bigPC/max, rung 2 = smallPC/high.
// The task is created before the config so the running dispatcher cannot
// pick it up before the failure count is set.
func (h *e2eHarness) seedLadderTask(t *testing.T, wfID string, failures int) (taskID, configID, primaryPC, bigPC string) {
	t.Helper()
	ctx := context.Background()
	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID}); err != nil {
		t.Fatal(err)
	}
	primaryPC = h.createProviderConfig(t, "fake", "cheap")
	bigPC = h.createProviderConfig(t, "fake", "big")
	taskID = uuid.NewString()
	if _, err := h.q.CreateTask(ctx, gen.CreateTaskParams{ID: taskID, Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.sqlDB.ExecContext(ctx, "UPDATE tasks SET consecutive_failure_count = ? WHERE id = ?", failures, taskID); err != nil {
		t.Fatal(err)
	}
	configID = uuid.NewString()
	if _, err := h.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: configID, Name: "ladder", ProviderConfigID: primaryPC, Effort: "low",
		EscalationLadder: `[{"provider_config_id":"` + bigPC + `","effort":"max"},{"provider_config_id":"` + primaryPC + `","effort":"high"}]`,
		Labels:           `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1,
	}); err != nil {
		t.Fatal(err)
	}
	return
}

func (h *e2eHarness) firstRun(t *testing.T, taskID string) gen.AgentRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runs, _ := h.q.ListAgentRuns(context.Background(), taskID)
		if len(runs) > 0 {
			return runs[len(runs)-1]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no run created")
	return gen.AgentRun{}
}

func TestE2E_Ladder_RungSelection(t *testing.T) {
	for _, c := range []struct {
		failures   int
		rung       int64
		effort     string
		bigPrimary bool
	}{{0, 0, "low", false}, {1, 1, "max", true}, {2, 2, "high", false}, {7, 2, "high", false}} {
		fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
		h := newE2EHarness(t, fp)
		wfID := seedE2EWorkflow(t, h.q)
		taskID, _, primaryPC, bigPC := h.seedLadderTask(t, wfID, c.failures)
		run := h.firstRun(t, taskID)
		if run.LadderRung == nil || *run.LadderRung != c.rung {
			t.Errorf("failures=%d: rung = %v want %d", c.failures, run.LadderRung, c.rung)
		}
		wantPC := primaryPC
		if c.bigPrimary {
			wantPC = bigPC
		}
		if run.ProviderConfigID == nil || *run.ProviderConfigID != wantPC {
			t.Errorf("failures=%d: provider = %v want %s", c.failures, run.ProviderConfigID, wantPC)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			fp.mu.Lock()
			n := len(fp.inputs)
			fp.mu.Unlock()
			if n > 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if got := fp.input(t, 0).AgentConfig.Effort; got != c.effort {
			t.Errorf("failures=%d: effort = %q want %q", c.failures, got, c.effort)
		}
	}
}

func TestE2E_Ladder_RateLimitedRungProviderFallsBack(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	h.disp.RateLimits = NewRateLimitRegistry()
	wfID := seedE2EWorkflow(t, h.q)
	taskID, _, primaryPC, bigPC := h.seedLadderTask(t, wfID, 1)
	h.disp.RateLimits.Block(providerPool(t, h.q, bigPC), time.Now().Add(time.Hour))
	run := h.firstRun(t, taskID)
	if run.LadderRung == nil || *run.LadderRung != 1 {
		t.Errorf("rung = %v want 1", run.LadderRung)
	}
	if run.ProviderConfigID == nil || *run.ProviderConfigID != primaryPC {
		t.Errorf("provider = %v want primary %s", run.ProviderConfigID, primaryPC)
	}
}

func TestE2E_NoLadder_RungNull(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID, _, _, _ := h.seedTaskWithProviderList(t, wfID)
	run := h.firstRun(t, taskID)
	if run.LadderRung != nil {
		t.Errorf("ladder_rung = %v, want NULL", *run.LadderRung)
	}
}
