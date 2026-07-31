package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestDashboardGet_ClaudeUsageUnavailableWithoutCredentials verifies the
// dashboard endpoint still returns 200 and reports claude_usage.available
// == false when no Claude OAuth credentials are present in the environment
// (the common CI/test case) — the live Anthropic usage fetch must never
// fail or block the overall /dashboard response.
func TestDashboardGet_ClaudeUsageUnavailableWithoutCredentials(t *testing.T) {
	// Point HOME somewhere without a ~/.claude/.credentials.json so
	// providers.ClaudeOAuthAccessToken() reliably returns "".
	t.Setenv("HOME", t.TempDir())
	_ = os.Unsetenv("ANTHROPIC_AUTH_TOKEN")

	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewDashboardHandler(q, 5)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		ClaudeUsage struct {
			Available       bool    `json:"available"`
			FiveHourPercent float64 `json:"five_hour_percent"`
			WeeklyPercent   float64 `json:"weekly_percent"`
		} `json:"claude_usage"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ClaudeUsage.Available {
		t.Errorf("expected claude_usage.available=false without credentials, got true")
	}
	if body.ClaudeUsage.FiveHourPercent != 0 || body.ClaudeUsage.WeeklyPercent != 0 {
		t.Errorf("expected zero-value percentages when unavailable, got %+v", body.ClaudeUsage)
	}
}

// TestDashboardGet_AgentConfigStats exercises the per-agent-config analytics
// table end to end: it seeds one agent config with a completed run (task
// ends on the terminal "done" label, with one transient retry recorded) and
// one failed run (task left on a non-terminal label), then asserts the
// aggregated success rate, duration, turns-to-done, retry snapshot, and
// token/cost fields all come back as expected.
func TestDashboardGet_AgentConfigStats(t *testing.T) {
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

	// Task 1: reaches the terminal "done" label after one transient retry,
	// with a single completed run under cfg.
	doneTask, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "done task", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create done task: %v", err)
	}
	if _, err := q.SetTaskTransientRetry(ctx, gen.SetTaskTransientRetryParams{
		TransientRetryCount: 1, ID: doneTask.ID,
	}); err != nil {
		t.Fatalf("set transient retry: %v", err)
	}
	completedRun, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID: uuid.NewString(), TaskID: doneTask.ID, AgentConfigID: &cfg.ID,
	})
	if err != nil {
		t.Fatalf("create completed run: %v", err)
	}
	// Set explicit started/completed timestamps (10s apart) so avg/p90
	// duration are deterministic instead of racing CURRENT_TIMESTAMP.
	started := time.Now().Add(-10 * time.Second)
	completed := time.Now()
	if _, err := db.SQL().ExecContext(ctx,
		`UPDATE agent_runs SET status = 'completed', started_at = ?, completed_at = ?, input_tokens = 100, output_tokens = 50, cost_usd = 0.01 WHERE id = ?`,
		started, completed, completedRun.ID,
	); err != nil {
		t.Fatalf("finalize completed run: %v", err)
	}
	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{
		Label: "done", CurrentAgentRunID: &completedRun.ID, ID: doneTask.ID,
	}); err != nil {
		t.Fatalf("move done task to done label: %v", err)
	}

	// Task 2: stays on a non-terminal label ("work") with one failed run
	// under the same config — should count toward run/failed totals but not
	// toward turns-to-done or the retry snapshot (task never reached done).
	pendingTask, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "pending task", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create pending task: %v", err)
	}
	failedRun, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID: uuid.NewString(), TaskID: pendingTask.ID, AgentConfigID: &cfg.ID,
	})
	if err != nil {
		t.Fatalf("create failed run: %v", err)
	}
	if _, err := q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status: "failed", InputTokens: 20, OutputTokens: 10, CostUsd: 0.002, ID: failedRun.ID,
	}); err != nil {
		t.Fatalf("finalize failed run: %v", err)
	}

	h := handlers.NewDashboardHandler(q, 5)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		AgentConfigStats []struct {
			AgentConfigID       string  `json:"agent_config_id"`
			AgentName           string  `json:"agent_name"`
			Provider            string  `json:"provider"`
			RunCount            int64   `json:"run_count"`
			CompletedCount      int64   `json:"completed_count"`
			FailedCount         int64   `json:"failed_count"`
			WaitingHumanCount   int64   `json:"waiting_human_count"`
			SuccessRatePercent  float64 `json:"success_rate_percent"`
			AvgDurationSecs     float64 `json:"avg_duration_secs"`
			P90DurationSecs     float64 `json:"p90_duration_secs"`
			AvgTurnsToDone      float64 `json:"avg_turns_to_done"`
			AvgTransientRetries float64 `json:"avg_transient_retries"`
			TasksWithRetries    int64   `json:"tasks_with_retries"`
			InputTokens         int64   `json:"input_tokens"`
			OutputTokens        int64   `json:"output_tokens"`
			CostUSD             float64 `json:"cost_usd"`
		} `json:"agent_config_stats"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(body.AgentConfigStats) != 1 {
		t.Fatalf("expected 1 agent config stats row, got %d: %+v", len(body.AgentConfigStats), body.AgentConfigStats)
	}
	row := body.AgentConfigStats[0]

	if row.AgentConfigID != cfg.ID || row.AgentName != "worker" || row.Provider != "claude" {
		t.Errorf("unexpected identity fields: %+v", row)
	}
	if row.RunCount != 2 {
		t.Errorf("expected run_count=2, got %d", row.RunCount)
	}
	if row.CompletedCount != 1 || row.FailedCount != 1 || row.WaitingHumanCount != 0 {
		t.Errorf("unexpected outcome counts: completed=%d failed=%d waiting_human=%d",
			row.CompletedCount, row.FailedCount, row.WaitingHumanCount)
	}
	if row.SuccessRatePercent != 50 {
		t.Errorf("expected success_rate_percent=50, got %v", row.SuccessRatePercent)
	}
	// Only the completed run has both started_at/completed_at set (~10s
	// apart); the failed run has neither, so it's excluded from duration
	// averaging per RunStatsByAgentConfig's filtering.
	if row.AvgDurationSecs < 9 || row.AvgDurationSecs > 11 {
		t.Errorf("expected avg_duration_secs ~10, got %v", row.AvgDurationSecs)
	}
	if row.P90DurationSecs < 9 || row.P90DurationSecs > 11 {
		t.Errorf("expected p90_duration_secs ~10, got %v", row.P90DurationSecs)
	}
	// Only doneTask counts toward turns-to-done/retries (it's the only task
	// on a terminal label); it had exactly 1 run and 1 transient retry.
	if row.AvgTurnsToDone != 1 {
		t.Errorf("expected avg_turns_to_done=1, got %v", row.AvgTurnsToDone)
	}
	if row.AvgTransientRetries != 1 {
		t.Errorf("expected avg_transient_retries=1, got %v", row.AvgTransientRetries)
	}
	if row.TasksWithRetries != 1 {
		t.Errorf("expected tasks_with_retries=1, got %d", row.TasksWithRetries)
	}
	if row.InputTokens != 120 || row.OutputTokens != 60 {
		t.Errorf("unexpected token totals: input=%d output=%d", row.InputTokens, row.OutputTokens)
	}
	if row.CostUSD < 0.011 || row.CostUSD > 0.013 {
		t.Errorf("expected cost_usd ~0.012, got %v", row.CostUSD)
	}
}

// TestDashboardGet_InterventionQueueExcludesSupersededRuns verifies that a
// waiting_human run only appears in the dashboard's intervention_queue while
// it is still the task's active run. Replying to (or approving/rejecting) a
// waiting_human run dispatches a new run and repoints
// tasks.active_agent_run_id at it, but deliberately leaves the old run's
// status as 'waiting_human' as a historical record — that superseded run
// must not keep showing up as "needs your input" once a new run is active.
func TestDashboardGet_InterventionQueueExcludesSupersededRuns(t *testing.T) {
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

	// Task A: still has its waiting_human run as the active run (nobody has
	// replied yet) — should appear in the intervention queue.
	waitingTask, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "waiting task", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create waiting task: %v", err)
	}
	waitingRun, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID: uuid.NewString(), TaskID: waitingTask.ID,
	})
	if err != nil {
		t.Fatalf("create waiting run: %v", err)
	}
	if _, err := q.UpdateAgentRunStatus(ctx, gen.UpdateAgentRunStatusParams{
		Status: "waiting_human", ID: waitingRun.ID,
	}); err != nil {
		t.Fatalf("set waiting run status: %v", err)
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &waitingRun.ID, ActiveAgentRunID: &waitingRun.ID, ID: waitingTask.ID,
	}); err != nil {
		t.Fatalf("set waiting task active run: %v", err)
	}

	// Task B: had a waiting_human run that a human already replied to, which
	// dispatched a new run now sitting at 'running' and holding
	// active_agent_run_id. The old run keeps its 'waiting_human' status per
	// ReplyRun's documented behavior, but must be excluded here.
	repliedTask, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "replied task", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create replied task: %v", err)
	}
	supersededRun, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID: uuid.NewString(), TaskID: repliedTask.ID,
	})
	if err != nil {
		t.Fatalf("create superseded run: %v", err)
	}
	if _, err := q.UpdateAgentRunStatus(ctx, gen.UpdateAgentRunStatusParams{
		Status: "waiting_human", ID: supersededRun.ID,
	}); err != nil {
		t.Fatalf("set superseded run status: %v", err)
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
	newRun, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID: uuid.NewString(), TaskID: repliedTask.ID, AgentConfigID: &cfg.ID,
	})
	if err != nil {
		t.Fatalf("create new run: %v", err)
	}
	if _, err := q.SetAgentRunStarted(ctx, newRun.ID); err != nil {
		t.Fatalf("start new run: %v", err)
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &newRun.ID, ActiveAgentRunID: &newRun.ID, ID: repliedTask.ID,
	}); err != nil {
		t.Fatalf("set replied task active run: %v", err)
	}

	h := handlers.NewDashboardHandler(q, 5)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		ActiveAgents []struct {
			RunID  string `json:"run_id"`
			TaskID string `json:"task_id"`
		} `json:"active_agents"`
		InterventionQueue []struct {
			RunID  string `json:"run_id"`
			TaskID string `json:"task_id"`
		} `json:"intervention_queue"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(body.InterventionQueue) != 1 || body.InterventionQueue[0].RunID != waitingRun.ID {
		t.Errorf("expected intervention_queue to contain only the still-active waiting run %q, got %+v",
			waitingRun.ID, body.InterventionQueue)
	}
	if len(body.ActiveAgents) != 1 || body.ActiveAgents[0].RunID != newRun.ID {
		t.Errorf("expected active_agents to contain only the new run %q, got %+v",
			newRun.ID, body.ActiveAgents)
	}
}

// TestDashboardGet_RepoConcurrency covers the repo_concurrency breakdown
// (issue #255): a repo with an explicit max_concurrent_runs override reports
// that value as its limit, a repo with no override falls back to the
// server's global MAX_WORKERS, and a repo with zero in-flight runs is
// omitted entirely.
func TestDashboardGet_RepoConcurrency(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()

	wfs, err := q.ListWorkflows(ctx)
	if err != nil || len(wfs) == 0 {
		t.Fatalf("list workflows: %v", err)
	}
	wfID := wfs[0].ID

	// Repo A: explicit cap of 1, one in-flight run.
	cappedLimit := int64(1)
	cappedRepoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: cappedRepoID, Name: "capped-repo", Path: t.TempDir(), WorkflowID: &wfID,
		MaxConcurrentRuns: &cappedLimit,
	}); err != nil {
		t.Fatalf("create capped repo: %v", err)
	}
	cappedTask, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "capped task", WorkflowID: wfID, RepoID: cappedRepoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create capped task: %v", err)
	}
	cappedRun, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID: uuid.NewString(), TaskID: cappedTask.ID,
	})
	if err != nil {
		t.Fatalf("create capped run: %v", err)
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &cappedRun.ID, ActiveAgentRunID: &cappedRun.ID, ID: cappedTask.ID,
	}); err != nil {
		t.Fatalf("set capped task active run: %v", err)
	}

	// Repo B: no override, one in-flight run — falls back to global MAX_WORKERS.
	defaultRepoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: defaultRepoID, Name: "default-repo", Path: t.TempDir(), WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create default repo: %v", err)
	}
	defaultTask, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "default task", WorkflowID: wfID, RepoID: defaultRepoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create default task: %v", err)
	}
	defaultRun, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID: uuid.NewString(), TaskID: defaultTask.ID,
	})
	if err != nil {
		t.Fatalf("create default run: %v", err)
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &defaultRun.ID, ActiveAgentRunID: &defaultRun.ID, ID: defaultTask.ID,
	}); err != nil {
		t.Fatalf("set default task active run: %v", err)
	}

	// Repo C: no in-flight runs at all — must not appear in the response.
	idleRepoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: idleRepoID, Name: "idle-repo", Path: t.TempDir(), WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create idle repo: %v", err)
	}

	h := handlers.NewDashboardHandler(q, 5)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		RepoConcurrency []struct {
			RepoID   string `json:"repo_id"`
			RepoName string `json:"repo_name"`
			InUse    int64  `json:"in_use"`
			Limit    int    `json:"limit"`
		} `json:"repo_concurrency"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(body.RepoConcurrency) != 2 {
		t.Fatalf("expected 2 repo_concurrency rows (idle repo excluded), got %d: %+v",
			len(body.RepoConcurrency), body.RepoConcurrency)
	}

	byID := map[string]struct {
		InUse int64
		Limit int
	}{}
	for _, r := range body.RepoConcurrency {
		byID[r.RepoID] = struct {
			InUse int64
			Limit int
		}{r.InUse, r.Limit}
	}

	capped, ok := byID[cappedRepoID]
	if !ok {
		t.Fatalf("expected capped repo in response, got %+v", body.RepoConcurrency)
	}
	if capped.InUse != 1 || capped.Limit != 1 {
		t.Errorf("capped repo: expected in_use=1 limit=1 (repo-specific override), got in_use=%d limit=%d",
			capped.InUse, capped.Limit)
	}

	def, ok := byID[defaultRepoID]
	if !ok {
		t.Fatalf("expected default repo in response, got %+v", body.RepoConcurrency)
	}
	if def.InUse != 1 || def.Limit != 5 {
		t.Errorf("default repo: expected in_use=1 limit=5 (global MAX_WORKERS fallback), got in_use=%d limit=%d",
			def.InUse, def.Limit)
	}

	if _, ok := byID[idleRepoID]; ok {
		t.Errorf("expected idle repo (no in-flight runs) to be excluded from repo_concurrency")
	}
}

// TestDashboardCostByTask_GroupsByTask seeds two runs on one task and one run
// on another, then verifies CostByTask returns a per-task rollup summing
// tokens/cost across all of a task's runs (see SumUsageByTask).
func TestDashboardCostByTask_GroupsByTask(t *testing.T) {
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

	taskA, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "task A", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create task A: %v", err)
	}
	taskB, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "task B", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create task B: %v", err)
	}

	mkRun := func(taskID string, inTok, outTok int64, cost float64) {
		run, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: taskID})
		if err != nil {
			t.Fatalf("create run: %v", err)
		}
		if _, err := q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
			Status: "completed", InputTokens: inTok, OutputTokens: outTok, CostUsd: cost, ID: run.ID,
		}); err != nil {
			t.Fatalf("finalize run: %v", err)
		}
	}
	mkRun(taskA.ID, 100, 50, 0.01)
	mkRun(taskA.ID, 200, 75, 0.02)
	mkRun(taskB.ID, 10, 5, 0.001)

	h := handlers.NewDashboardHandler(q, 5)
	req := httptest.NewRequest(http.MethodGet, "/dashboard/cost-by-task", nil)
	w := httptest.NewRecorder()
	h.CostByTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var rows []struct {
		TaskID       string  `json:"task_id"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		CostUSD      float64 `json:"cost_usd"`
	}
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 task cost rows, got %d: %+v", len(rows), rows)
	}
	byID := make(map[string]struct {
		InputTokens  int64
		OutputTokens int64
		CostUSD      float64
	}, len(rows))
	for _, row := range rows {
		byID[row.TaskID] = struct {
			InputTokens  int64
			OutputTokens int64
			CostUSD      float64
		}{row.InputTokens, row.OutputTokens, row.CostUSD}
	}
	a, ok := byID[taskA.ID]
	if !ok {
		t.Fatalf("expected task A in response, got %+v", rows)
	}
	if a.InputTokens != 300 || a.OutputTokens != 125 {
		t.Errorf("task A: expected input=300 output=125, got input=%d output=%d", a.InputTokens, a.OutputTokens)
	}
	if a.CostUSD < 0.029 || a.CostUSD > 0.031 {
		t.Errorf("task A: expected cost_usd ~0.03, got %v", a.CostUSD)
	}
	b, ok := byID[taskB.ID]
	if !ok {
		t.Fatalf("expected task B in response, got %+v", rows)
	}
	if b.InputTokens != 10 || b.OutputTokens != 5 {
		t.Errorf("task B: expected input=10 output=5, got input=%d output=%d", b.InputTokens, b.OutputTokens)
	}
}

// TestDashboardCostByTask_EmptyWhenNoRuns verifies the endpoint returns an
// empty (not null) array when there are no agent runs at all.
func TestDashboardCostByTask_EmptyWhenNoRuns(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewDashboardHandler(q, 5)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/cost-by-task", nil)
	w := httptest.NewRecorder()
	h.CostByTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var rows []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected empty cost-by-task result, got %+v", rows)
	}
}
