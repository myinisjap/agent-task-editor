package agent

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// seedTaskWithBudgets is like seedTaskOnReady but sets the agent config's
// max_cost_usd (at creation, configBudget) and the task's max_cost_usd
// (via a follow-up UpdateTask, taskBudget). A 0 value leaves that budget
// unset (unlimited from that source) — see effectiveBudget.
//
// The task is created on the non-pickup "parked" label and only moved to
// "ready" by the caller's seedRunWithCost (or seedReady) once all cost state
// is in place. The harness starts the dispatcher (sweeping every 15ms) before
// a test seeds, so creating the task directly on "ready" here races the first
// sweep: it could dispatch a real run before the prior-cost row lands, seeing
// spent=0 and skipping the budget guard. Landing on a pickup label last closes
// that window deterministically.
func (h *e2eHarness) seedTaskWithBudgets(t *testing.T, wfID string, configBudget, taskBudget float64) (taskID, agentConfigID string) {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	pcID := h.createProviderConfig(t, "fake", "none")
	cfg, err := h.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "fake-agent", ProviderConfigID: pcID,
		Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1,
		MaxCostUsd: configBudget,
	})
	if err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	taskID = uuid.NewString()
	if _, err := h.q.CreateTask(ctx, gen.CreateTaskParams{
		ID: taskID, Title: "do the thing", WorkflowID: wfID, RepoID: repoID, Label: "parked",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if taskBudget != 0 {
		task, err := h.q.GetTask(ctx, taskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if _, err := h.q.UpdateTask(ctx, gen.UpdateTaskParams{
			Title: task.Title, Description: task.Description, Type: task.Type, RepoID: task.RepoID,
			MaxCostUsd: taskBudget, ID: taskID,
		}); err != nil {
			t.Fatalf("set task max_cost_usd: %v", err)
		}
	}
	return taskID, cfg.ID
}

// seedReady moves a task seeded by seedTaskWithBudgets onto the "ready"
// pickup label, the last seeding step so the dispatcher's first sweep of the
// task always observes complete cost state (see seedTaskWithBudgets). Callers
// that seed a prior-cost run use seedRunWithCost, which calls this for them;
// callers with no prior run (the "no budget set" path) call it directly.
func (h *e2eHarness) seedReady(t *testing.T, taskID string) {
	t.Helper()
	if _, err := h.q.UpdateTaskLabel(context.Background(), gen.UpdateTaskLabelParams{
		Label: "ready", ID: taskID,
	}); err != nil {
		t.Fatalf("move task to ready: %v", err)
	}
}

// seedRunWithCost inserts a completed agent_runs row for taskID under
// agentConfigID carrying the given cost_usd, standing in for a run that
// already happened before the sweep a test observes. Counted by
// SumTaskCost/effectiveBudget regardless of status (see runs.sql). It then
// moves the task onto the "ready" pickup label as the final step, so the
// dispatcher's first sweep of the task always sees this cost already recorded
// (see seedTaskWithBudgets for the race this closes).
func (h *e2eHarness) seedRunWithCost(t *testing.T, taskID, agentConfigID string, cost float64) {
	t.Helper()
	ctx := context.Background()
	runID := uuid.NewString()
	if _, err := h.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID: runID, TaskID: taskID, AgentConfigID: &agentConfigID,
	}); err != nil {
		t.Fatalf("create prior run: %v", err)
	}
	if _, err := h.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status: "completed", CostUsd: cost, ID: runID,
	}); err != nil {
		t.Fatalf("complete prior run: %v", err)
	}
	h.seedReady(t, taskID)
}

// TestE2E_CostBudget covers the dispatcher's pre-dispatch cost-budget guard:
// a task whose cumulative recorded run cost has met or exceeded its
// effective budget (min of nonzero task/agent-config max_cost_usd) is never
// submitted to the pool. Instead the dispatcher creates a "phantom"
// waiting_human run (no provider invocation), locks the task on it, and
// publishes task.needs_human with a "budget exhausted: $X of $Y" message —
// mirroring Pool.handleTransientFailure's escalation shape.
func TestE2E_CostBudget(t *testing.T) {
	t.Run("no budget set: dispatches normally", func(t *testing.T) {
		fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
		h := newE2EHarness(t, fp)
		wfID := seedE2EWorkflow(t, h.q)
		taskID, _ := h.seedTaskWithBudgets(t, wfID, 0, 0)
		h.seedReady(t, taskID)

		h.pollTask(t, taskID, func(tk gen.Task) bool {
			return tk.Label == "next"
		}, "task to transition on the golden path with no budget set")
	})

	t.Run("agent config budget exceeded: escalates without dispatching", func(t *testing.T) {
		fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
		h := newE2EHarness(t, fp)
		wfID := seedE2EWorkflow(t, h.q)
		taskID, cfgID := h.seedTaskWithBudgets(t, wfID, 1.00, 0)
		h.seedRunWithCost(t, taskID, cfgID, 1.50)

		esc := h.pollTask(t, taskID, func(tk gen.Task) bool {
			return tk.ActiveAgentRunID != nil
		}, "task to be locked on a budget-exhausted phantom run")

		run, err := h.q.GetAgentRun(context.Background(), *esc.ActiveAgentRunID)
		if err != nil {
			t.Fatalf("get agent run: %v", err)
		}
		if run.Status != "waiting_human" {
			t.Fatalf("expected phantom run status 'waiting_human', got %q", run.Status)
		}
		wantMsg := "budget exhausted: $1.50 of $1.00"
		if run.Notes == nil || *run.Notes != wantMsg {
			t.Errorf("expected notes %q, got %v", wantMsg, run.Notes)
		}
		if esc.Label != "ready" {
			t.Errorf("expected task to stay on 'ready', got %q", esc.Label)
		}
		if !h.pub.has("task.needs_human") {
			t.Error("expected task.needs_human event on budget exhaustion")
		}
		if len(fp.inputs) != 0 {
			t.Errorf("expected the provider to never be invoked, got %d invocations", len(fp.inputs))
		}
	})

	t.Run("task budget exceeded: escalates without dispatching", func(t *testing.T) {
		fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
		h := newE2EHarness(t, fp)
		wfID := seedE2EWorkflow(t, h.q)
		taskID, cfgID := h.seedTaskWithBudgets(t, wfID, 0, 2.00)
		h.seedRunWithCost(t, taskID, cfgID, 2.00)

		esc := h.pollTask(t, taskID, func(tk gen.Task) bool {
			return tk.ActiveAgentRunID != nil
		}, "task to be locked on a budget-exhausted phantom run")
		run, err := h.q.GetAgentRun(context.Background(), *esc.ActiveAgentRunID)
		if err != nil {
			t.Fatalf("get agent run: %v", err)
		}
		if run.Status != "waiting_human" {
			t.Fatalf("expected phantom run status 'waiting_human', got %q", run.Status)
		}
		if len(fp.inputs) != 0 {
			t.Errorf("expected the provider to never be invoked, got %d invocations", len(fp.inputs))
		}
	})

	t.Run("min of both nonzero budgets wins", func(t *testing.T) {
		fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
		h := newE2EHarness(t, fp)
		wfID := seedE2EWorkflow(t, h.q)
		// config budget 5.00, task budget 1.00 -> effective is 1.00
		taskID, cfgID := h.seedTaskWithBudgets(t, wfID, 5.00, 1.00)
		h.seedRunWithCost(t, taskID, cfgID, 1.00)

		esc := h.pollTask(t, taskID, func(tk gen.Task) bool {
			return tk.ActiveAgentRunID != nil
		}, "task to be locked on a budget-exhausted phantom run using the lower of the two budgets")
		run, err := h.q.GetAgentRun(context.Background(), *esc.ActiveAgentRunID)
		if err != nil {
			t.Fatalf("get agent run: %v", err)
		}
		wantMsg := "budget exhausted: $1.00 of $1.00"
		if run.Notes == nil || *run.Notes != wantMsg {
			t.Errorf("expected notes %q (effective budget = min(5.00, 1.00)), got %v", wantMsg, run.Notes)
		}
	})

	t.Run("budget not yet exceeded: dispatches normally", func(t *testing.T) {
		fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
		h := newE2EHarness(t, fp)
		wfID := seedE2EWorkflow(t, h.q)
		taskID, cfgID := h.seedTaskWithBudgets(t, wfID, 10.00, 0)
		h.seedRunWithCost(t, taskID, cfgID, 1.00) // well under the 10.00 config budget

		h.pollTask(t, taskID, func(tk gen.Task) bool {
			return tk.Label == "next"
		}, "task to transition normally when under budget")
	})

	t.Run("RunInput carries the effective budget, prior spend, and default warn ratio", func(t *testing.T) {
		fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
		h := newE2EHarness(t, fp)
		wfID := seedE2EWorkflow(t, h.q)
		// config budget 5.00, task budget 2.00 -> effective is 2.00; prior spend 1.00 (under budget).
		taskID, cfgID := h.seedTaskWithBudgets(t, wfID, 5.00, 2.00)
		h.seedRunWithCost(t, taskID, cfgID, 1.00)

		h.pollTask(t, taskID, func(tk gen.Task) bool {
			return tk.Label == "next"
		}, "task to dispatch and transition under budget")

		in := fp.input(t, 0)
		if in.CostBudgetUSD != 2.00 {
			t.Errorf("expected RunInput.CostBudgetUSD 2.00 (effective budget), got %v", in.CostBudgetUSD)
		}
		if in.CostSpentUSD != 1.00 {
			t.Errorf("expected RunInput.CostSpentUSD 1.00 (prior recorded run cost), got %v", in.CostSpentUSD)
		}
		if in.CostWarnRatio != defaultCostWarnRatio {
			t.Errorf("expected RunInput.CostWarnRatio to default to %v, got %v", defaultCostWarnRatio, in.CostWarnRatio)
		}
	})

	t.Run("no budget set: RunInput carries a zero CostBudgetUSD (watchdog inert)", func(t *testing.T) {
		fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
		h := newE2EHarness(t, fp)
		wfID := seedE2EWorkflow(t, h.q)
		taskID, _ := h.seedTaskWithBudgets(t, wfID, 0, 0)
		h.seedReady(t, taskID)

		h.pollTask(t, taskID, func(tk gen.Task) bool {
			return tk.Label == "next"
		}, "task to transition on the golden path with no budget set")

		in := fp.input(t, 0)
		if in.CostBudgetUSD != 0 {
			t.Errorf("expected RunInput.CostBudgetUSD 0 (no cap), got %v", in.CostBudgetUSD)
		}
	})

	// TestE2E_CostBudget's other subtests exercise the *hard* pre-dispatch gate
	// (spent >= budget). This covers the *early-warning* gate one rung below
	// it (see checkCostBudget): a task whose prior spend has crossed
	// warnRatio*budget but not yet the budget itself still dispatches
	// normally, but the dispatcher publishes a one-shot task.cost_warning and
	// sets the task's cost_warned flag so it doesn't refire every sweep.
	t.Run("warning threshold crossed but budget not exhausted: dispatches, warns once", func(t *testing.T) {
		fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
		h := newE2EHarness(t, fp)
		wfID := seedE2EWorkflow(t, h.q)
		// effective budget 10.00; prior spend 9.00 = 90% > the 80% default warn ratio.
		taskID, cfgID := h.seedTaskWithBudgets(t, wfID, 0, 10.00)
		h.seedRunWithCost(t, taskID, cfgID, 9.00)

		h.pollTask(t, taskID, func(tk gen.Task) bool {
			return tk.Label == "next"
		}, "task to transition normally despite crossing the warn threshold")

		if !h.pub.has("task.cost_warning") {
			t.Error("expected a task.cost_warning event for a task past the warn threshold")
		}
		tk, err := h.q.GetTask(context.Background(), taskID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if tk.CostWarned == 0 {
			t.Error("expected task.cost_warned to be set after crossing the warn threshold")
		}
	})

	t.Run("below warning threshold: dispatches, no warning", func(t *testing.T) {
		fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
		h := newE2EHarness(t, fp)
		wfID := seedE2EWorkflow(t, h.q)
		// effective budget 10.00; prior spend 1.00 = 10%, well under the 80% default.
		taskID, cfgID := h.seedTaskWithBudgets(t, wfID, 0, 10.00)
		h.seedRunWithCost(t, taskID, cfgID, 1.00)

		h.pollTask(t, taskID, func(tk gen.Task) bool {
			return tk.Label == "next"
		}, "task to transition normally under the warn threshold")

		if h.pub.has("task.cost_warning") {
			t.Error("expected no task.cost_warning event for a task well under the warn threshold")
		}
	})
}

// TestE2E_MidRunCostKillSwitch_EscalatesAndStaysLocked covers the mid-run
// kill switch's pool-side escalation path (see providers/cost_watchdog.go and
// pool.handleCostBudgetExceeded): a provider that cancels its own run and
// returns agent.ErrCostBudgetExceeded is escalated straight to waiting_human,
// exactly like max-turns exhaustion — never re-dispatched with a fresh
// budget. This test exercises the pool's escalation given the typed error;
// the projection/cancellation logic itself lives in
// providers/cost_watchdog_test.go and the claude/qwen provider tests.
//
// It also covers the regression where a killed run's usage/cost never made
// it into the persisted agent_runs row: claude.go/qwen.go now populate
// Result.CostUSD/InputTokens/OutputTokens from the watchdog's own
// cumulative-usage snapshot (since a killed run never reaches its terminal
// "result" event for applyUsage to read), so this fake provider's Result
// mirrors that by setting them explicitly — pool.handleCostBudgetExceeded
// must persist them onto SetAgentRunCompleted, and SumTaskCost (the guard
// that a repeated kill/resume cycle relies on to not overspend) must see
// them.
func TestE2E_MidRunCostKillSwitch_EscalatesAndStaysLocked(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{
		{err: &ErrCostBudgetExceeded{SpentUSD: 12.34, BudgetUSD: 10.00}, result: Result{Status: "failed", SessionID: "sess-1", CostUSD: 12.34, InputTokens: 500_000, OutputTokens: 300_000}},
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReady(t, wfID)

	esc := h.pollTask(t, taskID, func(tk gen.Task) bool {
		return tk.ActiveAgentRunID != nil && tk.TransientRetryCount == 0
	}, "run to hit the cost budget and escalate to waiting_human")

	// active_agent_run_id is set synchronously at dispatch time, before the
	// fake provider even runs — pollTask above only proves the run started,
	// not that it finished. Poll the run row itself for its terminal status
	// before asserting on notes/cost/tokens, all of which are written
	// together by pool.handleCostBudgetExceeded once the run completes.
	run := h.pollAgentRun(t, *esc.ActiveAgentRunID, func(r gen.AgentRun) bool {
		return r.Status == "waiting_human"
	}, "run to be persisted as waiting_human after the cost-budget kill")
	wantMsg := "mid-run cost budget exceeded: $12.34 of $10.00"
	if run.Notes == nil || *run.Notes != wantMsg {
		t.Errorf("expected notes %q, got %v", wantMsg, run.Notes)
	}
	if esc.Label != "ready" {
		t.Errorf("expected task to stay on 'ready' while waiting_human, got %q", esc.Label)
	}
	if !h.pub.has("task.needs_human") {
		t.Error("expected task.needs_human event on mid-run cost budget exceeded")
	}

	// Regression: the killed run's own cost/tokens must be persisted, not
	// left at zero — otherwise SumTaskCost (which the pre-dispatch guard and
	// the task-detail cumulative-cost view both read) would silently
	// undercount this run's real spend, letting a kill/resume cycle
	// overspend the same "budget" repeatedly.
	if run.CostUsd != 12.34 {
		t.Errorf("expected run.CostUsd 12.34 (persisted from the watchdog's usage snapshot), got %v", run.CostUsd)
	}
	if run.InputTokens != 500_000 || run.OutputTokens != 300_000 {
		t.Errorf("expected run tokens (500000, 300000), got (%d, %d)", run.InputTokens, run.OutputTokens)
	}
	spent, err := h.q.SumTaskCost(context.Background(), taskID)
	if err != nil {
		t.Fatalf("sum task cost: %v", err)
	}
	if spent != 12.34 {
		t.Errorf("expected SumTaskCost to include the killed run's cost (12.34), got %v", spent)
	}

	// Same regression guard as TestE2E_MaxTurnsExhaustion_EscalatesAndStaysLocked:
	// the task must stay locked on this run indefinitely, not get re-picked
	// with a fresh budget on the next sweep.
	time.Sleep(200 * time.Millisecond)
	still, err := h.q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if still.ActiveAgentRunID == nil || *still.ActiveAgentRunID != *esc.ActiveAgentRunID {
		t.Fatalf("expected task to stay locked on run %q, got %v", *esc.ActiveAgentRunID, still.ActiveAgentRunID)
	}
	runs, err := h.q.ListAgentRuns(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("expected exactly 1 run after cost-budget exhaustion (no re-dispatch), got %d", len(runs))
	}
}

// TestE2E_CostWarned_PublishesEventEvenOnSuccessfulCompletion covers
// pool.run's Result.CostWarned handling: a provider's mid-run watchdog can
// cross the warning threshold and then still finish the run successfully
// under budget (e.g. a burst of tool calls early in a long run that tapers
// off) — the pool must still surface the warning to the board rather than
// only ever warning on a run that also fails or gets killed.
func TestE2E_CostWarned_PublishesEventEvenOnSuccessfulCompletion(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "completed", Outcome: "success", CostWarned: true, CostUSD: 8.50}},
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReady(t, wfID)

	h.pollTask(t, taskID, func(tk gen.Task) bool {
		return tk.Label == "next"
	}, "task to complete successfully despite a mid-run cost warning")

	if !h.pub.has("task.cost_warning") {
		t.Error("expected a task.cost_warning event even though the run ultimately completed successfully")
	}
}
