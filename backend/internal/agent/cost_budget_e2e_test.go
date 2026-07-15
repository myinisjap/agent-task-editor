package agent

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// seedTaskWithBudgets is like seedTaskOnReady but sets the agent config's
// max_cost_usd (at creation, configBudget) and the task's max_cost_usd
// (via a follow-up UpdateTask, taskBudget). A 0 value leaves that budget
// unset (unlimited from that source) — see effectiveBudget.
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
		ID: taskID, Title: "do the thing", WorkflowID: wfID, RepoID: repoID, Label: "ready",
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

// seedRunWithCost inserts a completed agent_runs row for taskID under
// agentConfigID carrying the given cost_usd, standing in for a run that
// already happened before the sweep a test observes. Counted by
// SumTaskCost/effectiveBudget regardless of status (see runs.sql).
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
}
