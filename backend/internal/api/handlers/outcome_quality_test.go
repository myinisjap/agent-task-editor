package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type outcomeQualityResponse struct {
	Configs []struct {
		AgentConfigID         string  `json:"agent_config_id"`
		AgentName             string  `json:"agent_name"`
		Provider              string  `json:"provider"`
		TasksDone             int64   `json:"tasks_done"`
		AvgCostToDoneUSD      float64 `json:"avg_cost_to_done_usd"`
		ReworkRatePercent     float64 `json:"rework_rate_percent"`
		ReworkN               int64   `json:"rework_n"`
		LowSampleRework       bool    `json:"low_sample_rework"`
		HumanTouchRatePercent float64 `json:"human_touch_rate_percent"`
		HumanTouchN           int64   `json:"human_touch_n"`
		LowSampleHumanTouch   bool    `json:"low_sample_human_touch"`
		AvgReviewComments     float64 `json:"avg_review_comments"`
		RunsFinished          int64   `json:"runs_finished"`
		EscalationRatePercent float64 `json:"escalation_rate_percent"`
		LowSampleEscalation   bool    `json:"low_sample_escalation"`
	} `json:"configs"`
}

func TestOutcomeQualityGet_CostReworkAndHumanTouch(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()

	wfs, err := q.ListWorkflows(ctx)
	if err != nil || len(wfs) == 0 {
		t.Fatalf("list workflows: %v", err)
	}
	wfID := wfs[0].ID

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: t.TempDir(), WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	pc, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{
		ID: uuid.NewString(), Name: "worker-provider", Provider: "claude", Model: "sonnet", Env: "{}",
	})
	if err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	cfg, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "worker", ProviderConfigID: pc.ID,
		Labels: `["work"]`, MaxTokens: 8192, TimeoutSecs: 600, MaxTurns: 50,
		EnabledPlugins: "[]", EnabledMcpServers: "[]", CommandAllowlist: "[]", CommandDenylist: "[]",
		MaxRetries: 3, RetryBackoffSecs: 30, ResumeSessions: 1, SubtasksEnabled: 0, MaxSubtasks: 10,
	})
	if err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	// Task 1: bounced from "review" back to "work" (rework: task revisits
	// "work"), then eventually reaches "done". Two runs under cfg, total
	// cost 0.05. One review comment. One human-triggered transition
	// (review -> work is a human reject in the default workflow).
	task1, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "reworked task", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create task1: %v", err)
	}

	run1, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: task1.ID, AgentConfigID: &cfg.ID})
	if err != nil {
		t.Fatalf("create run1: %v", err)
	}
	if _, err := q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status: "completed", InputTokens: 10, OutputTokens: 5, CostUsd: 0.02, ID: run1.ID,
	}); err != nil {
		t.Fatalf("complete run1: %v", err)
	}

	// Insert label history rows with explicit, increasing timestamps so
	// ordering is deterministic: work -> review -> work (revisit: rework) -> done.
	// base is set after run1/run2 are created (their created_at is
	// CURRENT_TIMESTAMP, i.e. "now") so precedingRunConfig can find them as
	// having happened before each backdated history entry.
	base := time.Now().Add(time.Minute)
	insertHistory := func(fromLabel *string, toLabel string, trigger string, at time.Time) {
		id := uuid.NewString()
		if _, err := db.SQL().ExecContext(ctx,
			`INSERT INTO task_label_history (id, task_id, from_label, to_label, trigger, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, task1.ID, fromLabel, toLabel, trigger, at,
		); err != nil {
			t.Fatalf("insert history: %v", err)
		}
	}
	workLabel := "work"
	reviewLabel := "review"
	insertHistory(nil, "work", "agent", base)
	insertHistory(&workLabel, "review", "agent", base.Add(1*time.Minute))
	insertHistory(&reviewLabel, "work", "human", base.Add(2*time.Minute)) // rework: revisits "work"

	run2, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: task1.ID, AgentConfigID: &cfg.ID})
	if err != nil {
		t.Fatalf("create run2: %v", err)
	}
	if _, err := q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status: "completed", InputTokens: 20, OutputTokens: 10, CostUsd: 0.03, ID: run2.ID,
	}); err != nil {
		t.Fatalf("complete run2: %v", err)
	}
	insertHistory(&workLabel, "done", "agent", base.Add(3*time.Minute))

	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{
		Label: "done", CurrentAgentRunID: &run2.ID, ID: task1.ID,
	}); err != nil {
		t.Fatalf("move task1 to done: %v", err)
	}

	if _, err := q.CreateTaskReviewComment(ctx, gen.CreateTaskReviewCommentParams{
		ID: uuid.NewString(), TaskID: task1.ID, FilePath: "main.go", Side: "new",
		StartLine: 1, EndLine: 1, QuotedText: "x", Body: "fix this",
	}); err != nil {
		t.Fatalf("create review comment: %v", err)
	}

	// Task 2: clean run, no rework, no human touch, reaches done.
	task2, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "clean task", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create task2: %v", err)
	}
	run3, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: task2.ID, AgentConfigID: &cfg.ID})
	if err != nil {
		t.Fatalf("create run3: %v", err)
	}
	if _, err := q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status: "completed", InputTokens: 5, OutputTokens: 5, CostUsd: 0.01, ID: run3.ID,
	}); err != nil {
		t.Fatalf("complete run3: %v", err)
	}
	insertHistory2 := func(taskID string, fromLabel *string, toLabel string, trigger string, at time.Time) {
		id := uuid.NewString()
		if _, err := db.SQL().ExecContext(ctx,
			`INSERT INTO task_label_history (id, task_id, from_label, to_label, trigger, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, taskID, fromLabel, toLabel, trigger, at,
		); err != nil {
			t.Fatalf("insert history: %v", err)
		}
	}
	insertHistory2(task2.ID, nil, "work", "agent", base)
	insertHistory2(task2.ID, &workLabel, "done", "agent", base.Add(1*time.Minute))
	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{
		Label: "done", CurrentAgentRunID: &run3.ID, ID: task2.ID,
	}); err != nil {
		t.Fatalf("move task2 to done: %v", err)
	}

	// Task 3: never finishes (stays on "work"), should not count toward
	// tasks_done at all despite having runs/cost.
	task3, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "pending task", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create task3: %v", err)
	}
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: task3.ID, AgentConfigID: &cfg.ID}); err != nil {
		t.Fatalf("create run for task3: %v", err)
	}

	h := handlers.NewOutcomeQualityHandler(q)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/outcome-quality", nil)
	w := httptest.NewRecorder()
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body outcomeQualityResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Configs) != 1 {
		t.Fatalf("expected 1 config row, got %d: %+v", len(body.Configs), body.Configs)
	}
	row := body.Configs[0]

	if row.AgentConfigID != cfg.ID || row.AgentName != "worker" || row.Provider != "claude" {
		t.Errorf("unexpected identity fields: %+v", row)
	}
	// Only task1 and task2 reached "done"; task3 is excluded.
	if row.TasksDone != 2 {
		t.Errorf("expected tasks_done=2, got %d", row.TasksDone)
	}
	// avg cost to done = (0.02+0.03 + 0.01) / 2 = 0.03
	if row.AvgCostToDoneUSD < 0.0299 || row.AvgCostToDoneUSD > 0.0301 {
		t.Errorf("expected avg_cost_to_done_usd ~0.03, got %v", row.AvgCostToDoneUSD)
	}
	// rework: 1 event across 2 finished tasks = 50%
	if row.ReworkN != 2 {
		t.Errorf("expected rework_n=2, got %d", row.ReworkN)
	}
	if row.ReworkRatePercent < 49 || row.ReworkRatePercent > 51 {
		t.Errorf("expected rework_rate_percent ~50, got %v", row.ReworkRatePercent)
	}
	if !row.LowSampleRework {
		t.Errorf("expected low_sample_rework=true with n=2 (< minSampleSizeForRate)")
	}
	// human touch: only task1 had a human-trigger transition = 50%
	if row.HumanTouchN != 2 {
		t.Errorf("expected human_touch_n=2, got %d", row.HumanTouchN)
	}
	if row.HumanTouchRatePercent < 49 || row.HumanTouchRatePercent > 51 {
		t.Errorf("expected human_touch_rate_percent ~50, got %v", row.HumanTouchRatePercent)
	}
	// review comments: 1 across 2 tasks = avg 0.5
	if row.AvgReviewComments < 0.49 || row.AvgReviewComments > 0.51 {
		t.Errorf("expected avg_review_comments ~0.5, got %v", row.AvgReviewComments)
	}
	// escalation: all 3 finished runs (run1, run2, run3) completed, none waiting_human.
	if row.RunsFinished != 3 {
		t.Errorf("expected runs_finished=3 (run3 from task3's pending run excluded, it's not terminal-run-status), got %d", row.RunsFinished)
	}
	if row.EscalationRatePercent != 0 {
		t.Errorf("expected escalation_rate_percent=0, got %v", row.EscalationRatePercent)
	}
}

// TestOutcomeQualityGet_RepoFilter verifies ?repo_id= scopes every metric to
// tasks in that repo only, recomputing rather than reusing the cached
// unfiltered snapshot.
func TestOutcomeQualityGet_RepoFilter(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()

	wfs, err := q.ListWorkflows(ctx)
	if err != nil || len(wfs) == 0 {
		t.Fatalf("list workflows: %v", err)
	}
	wfID := wfs[0].ID

	repoA := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoA, Name: "repo-a", Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
		t.Fatalf("create repoA: %v", err)
	}
	repoB := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoB, Name: "repo-b", Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
		t.Fatalf("create repoB: %v", err)
	}

	pc, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{
		ID: uuid.NewString(), Name: "worker-provider", Provider: "claude", Model: "sonnet", Env: "{}",
	})
	if err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	cfg, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "worker", ProviderConfigID: pc.ID,
		Labels: `["work"]`, MaxTokens: 8192, TimeoutSecs: 600, MaxTurns: 50,
		EnabledPlugins: "[]", EnabledMcpServers: "[]", CommandAllowlist: "[]", CommandDenylist: "[]",
		MaxRetries: 3, RetryBackoffSecs: 30, ResumeSessions: 1, SubtasksEnabled: 0, MaxSubtasks: 10,
	})
	if err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	taskA, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: "task a", WorkflowID: wfID, RepoID: repoA, Label: "done"})
	if err != nil {
		t.Fatalf("create taskA: %v", err)
	}
	runA, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: taskA.ID, AgentConfigID: &cfg.ID})
	if err != nil {
		t.Fatalf("create runA: %v", err)
	}
	if _, err := q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{Status: "completed", CostUsd: 1.0, ID: runA.ID}); err != nil {
		t.Fatalf("complete runA: %v", err)
	}

	taskB, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: "task b", WorkflowID: wfID, RepoID: repoB, Label: "done"})
	if err != nil {
		t.Fatalf("create taskB: %v", err)
	}
	runB, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: taskB.ID, AgentConfigID: &cfg.ID})
	if err != nil {
		t.Fatalf("create runB: %v", err)
	}
	if _, err := q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{Status: "completed", CostUsd: 5.0, ID: runB.ID}); err != nil {
		t.Fatalf("complete runB: %v", err)
	}

	h := handlers.NewOutcomeQualityHandler(q)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/outcome-quality?repo_id="+repoA, nil)
	w := httptest.NewRecorder()
	h.Get(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body outcomeQualityResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Configs) != 1 {
		t.Fatalf("expected 1 config row, got %d", len(body.Configs))
	}
	if body.Configs[0].TasksDone != 1 {
		t.Errorf("expected tasks_done=1 for repoA filter, got %d", body.Configs[0].TasksDone)
	}
	if body.Configs[0].AvgCostToDoneUSD < 0.99 || body.Configs[0].AvgCostToDoneUSD > 1.01 {
		t.Errorf("expected avg_cost_to_done_usd ~1.0 for repoA filter, got %v", body.Configs[0].AvgCostToDoneUSD)
	}
}
