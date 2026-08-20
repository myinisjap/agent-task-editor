package handlers

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent/providers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// claudeUsageCacheTTL bounds how often the dashboard endpoint will call out
// to Anthropic's OAuth usage endpoint. The dashboard page refetches on
// several WS events (task label/agent state changes), which can be
// frequent during active runs — without a cache this would hammer
// Anthropic's usage endpoint.
const claudeUsageCacheTTL = 45 * time.Second

// claudeUsageRateLimitedCacheTTL is the cache TTL applied when the last
// fetch got a 429 from Anthropic's usage endpoint. A 429 means Anthropic is
// itself rate-limiting the usage endpoint, so respect it by caching the
// unavailable result far longer than the normal success TTL rather than
// re-poking every dashboard load.
const claudeUsageRateLimitedCacheTTL = 10 * time.Minute

type DashboardHandler struct {
	q *gen.Queries
	// maxWorkers is the global MAX_WORKERS setting, used as the effective
	// per-repo concurrency limit for any repo with no repos.max_concurrent_runs
	// override (see repoConcurrency).
	maxWorkers int
	// globalCost supplies the dispatcher's cached global daily/monthly
	// spend-ceiling snapshot (see GlobalCostReporter), surfaced in the
	// dashboard response alongside a simple burn-rate forecast (see
	// globalCostBudget). May be nil (e.g. in tests), in which case the
	// dashboard response's global_cost_budget field is omitted.
	globalCost GlobalCostReporter

	usageMu       sync.Mutex
	cachedUsage   claudeUsageResponse
	cachedUsageAt time.Time
}

func NewDashboardHandler(q *gen.Queries, maxWorkers int, globalCost GlobalCostReporter) *DashboardHandler {
	return &DashboardHandler{q: q, maxWorkers: maxWorkers, globalCost: globalCost}
}

type dashboardResponse struct {
	LabelCounts       map[string]int        `json:"label_counts"`
	ActiveAgents      []activeAgentRow      `json:"active_agents"`
	InterventionQueue []interventionRow     `json:"intervention_queue"`
	CostTotal         costTotals            `json:"cost_total"`
	CostByProvider    []providerCostRow     `json:"cost_by_provider"`
	AgentConfigStats  []agentConfigStatRow  `json:"agent_config_stats"`
	CostByDay         []costByDayRow        `json:"cost_by_day"`
	CostByTask        []taskCostRow         `json:"cost_by_task"`
	CostByRepo        []repoCostRow         `json:"cost_by_repo"`
	ClaudeUsage       claudeUsageResponse   `json:"claude_usage"`
	RepoConcurrency   []repoConcurrencyRow  `json:"repo_concurrency"`
	GlobalCostBudget  *globalCostBudgetView `json:"global_cost_budget,omitempty"`
}

// repoCostRow is a per-repo token/cost rollup (see SumUsageByRepo), the
// natural companion to CostByTask/CostByProvider for answering "which repo
// is expensive" -- the intended input to setting a per-repo
// repos.max_concurrent_runs limit.
type repoCostRow struct {
	RepoID       string  `json:"repo_id"`
	RepoName     string  `json:"repo_name"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	RunCount     int64   `json:"run_count"`
}

// globalCostBudgetView is the dashboard's rendering of the dispatcher's
// global daily/monthly spend ceiling (see agent.GlobalCostStatus) plus a
// simple "projected spend at current burn" forecast per period, deliberately
// unsophisticated (trailing 7-day mean extrapolated linearly to the end of
// the period) -- the goal is "am I on track to blow through this", not an
// accurate prediction. Only present when at least one of MaxDailyCostUSD/
// MaxMonthlyCostUSD is configured (a forecast against an unlimited budget is
// meaningless).
type globalCostBudgetView struct {
	agent.GlobalCostStatus
	DailyForecastUSD   *float64 `json:"daily_forecast_usd,omitempty"`
	MonthlyForecastUSD *float64 `json:"monthly_forecast_usd,omitempty"`
}

// repoConcurrencyRow is the per-repo worker-slot breakdown: how many of a
// repo's effective concurrency limit are currently occupied by in-flight
// agent runs. Limit is repos.max_concurrent_runs when the repo has one set,
// otherwise the pool's global MAX_WORKERS (the same fallback the dispatcher
// enforces — see Dispatcher.repoAtLimit). Only repos with at least one
// in-flight run are included, keeping this list proportional to current
// activity rather than listing every repo on every poll.
type repoConcurrencyRow struct {
	RepoID   string `json:"repo_id"`
	RepoName string `json:"repo_name"`
	InUse    int64  `json:"in_use"`
	Limit    int    `json:"limit"`
}

// claudeUsageResponse is the live Claude account rate-limit utilization
// (5-hour rolling window + weekly window) from Anthropic's OAuth usage
// endpoint. Available is false when the server has no Claude OAuth
// credentials (~/.claude/.credentials.json) or the fetch failed for any
// other reason — this must never fail the /dashboard request as a whole.
type claudeUsageResponse struct {
	Available        bool       `json:"available"`
	RateLimited      bool       `json:"rate_limited,omitempty"`
	FiveHourPercent  float64    `json:"five_hour_percent"`
	FiveHourResetsAt *time.Time `json:"five_hour_resets_at,omitempty"`
	WeeklyPercent    float64    `json:"weekly_percent"`
	WeeklyResetsAt   *time.Time `json:"weekly_resets_at,omitempty"`
}

// costTotals holds token/cost usage totals across all completed (or
// terminal-state: completed/failed/waiting_human) agent runs.
type costTotals struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// providerCostRow is a per-provider breakdown of token/cost usage. Runs
// whose agent_config was later deleted (agent_config_id set NULL) are
// excluded from this breakdown since they can no longer be attributed to a
// provider — see SumUsageByProvider in runs.sql.
type providerCostRow struct {
	Provider     string  `json:"provider"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	RunCount     int64   `json:"run_count"`
}

// agentConfigStatRow is a per-agent-config breakdown of run outcomes,
// duration, turns used vs. the configured cap, runs-to-done,
// transient-retry frequency, and token/cost usage.
// It answers "which agent config is actually performing?" by combining
// agent_runs (status/duration/tokens/cost) with tasks.transient_retry_count.
//
// Two caveats apply and are surfaced in docs/api.md and docs/agents.md:
//  1. AvgRunsPerTask is a proportional split: each done task contributes
//     1.0 "task credit" divided across every agent config that ran on it,
//     weighted by that config's share of the task's total runs (e.g. if a
//     task was retried twice under agent A then finished by agent B in one
//     run, A gets 2/3 of a task credit and 2 runs, B gets 1/3 of a task
//     credit and 1 run). The retry fields, by contrast, are still
//     attributed entirely to a task's *last* run's agent config.
//  2. TasksWithRetries/AvgTransientRetries are a live snapshot of
//     tasks.transient_retry_count, which resets to 0 on success or
//     escalation to a human — this is NOT a lifetime/historical retry
//     count, just "how many tasks currently sitting done have a nonzero
//     retry count right now".
type agentConfigStatRow struct {
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
	AvgTurnsUsed        float64 `json:"avg_turns_used"`
	P90TurnsUsed        float64 `json:"p90_turns_used"`
	MaxTurns            int64   `json:"max_turns"`
	AvgRunsPerTask      float64 `json:"avg_runs_per_task"`
	AvgTransientRetries float64 `json:"avg_transient_retries"`
	TasksWithRetries    int64   `json:"tasks_with_retries"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CostUSD             float64 `json:"cost_usd"`
}

// costByDayRow is a daily rollup of token/cost usage for the dashboard's
// cost-by-day breakdown, most recent day first (see SumUsageByDay). "Per
// week" is deliberately not a separate query — the day-level data is
// granular enough for a human to visually aggregate, and adding a second
// strftime-grouped query would be redundant for the same underlying rows.
type costByDayRow struct {
	Day          string  `json:"day"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	RunCount     int64   `json:"run_count"`
}

// taskCostRow is a per-task token/cost rollup (see SumUsageByTask), used
// both for the dashboard's "top tasks by cost" table and, via
// GET /dashboard/cost-by-task, for the board page's per-filter cost badge.
type taskCostRow struct {
	TaskID       string  `json:"task_id"`
	TaskTitle    string  `json:"task_title"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

type activeAgentRow struct {
	RunID     string `json:"run_id"`
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	AgentName string `json:"agent_name"`
	StartedAt string `json:"started_at"`
}

type interventionRow struct {
	RunID     string  `json:"run_id"`
	TaskID    string  `json:"task_id"`
	TaskTitle string  `json:"task_title"`
	Message   *string `json:"message"`
	CreatedAt string  `json:"created_at"`
}

func (h *DashboardHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Per-label counts of non-archived tasks, computed in SQL rather than by
	// pulling every column (including the full description text) of every
	// task row via ListTasks.
	labelCounts, err := h.q.CountAllTasksByLabel(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	counts := make(map[string]int, len(labelCounts))
	for _, lc := range labelCounts {
		counts[lc.Label] = int(lc.Count)
	}

	activeRuns, err := h.q.ListActiveAgentRuns(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	activeRows := make([]activeAgentRow, 0, len(activeRuns))
	for _, r := range activeRuns {
		startedAt := ""
		if r.StartedAt != nil {
			startedAt = r.StartedAt.String()
		}
		activeRows = append(activeRows, activeAgentRow{
			RunID:     r.ID,
			TaskID:    r.TaskID,
			TaskTitle: r.TaskTitle,
			AgentName: r.AgentName,
			StartedAt: startedAt,
		})
	}

	waitingRuns, err := h.q.ListWaitingHumanRuns(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	interventionRows := make([]interventionRow, 0, len(waitingRuns))
	for _, r := range waitingRuns {
		interventionRows = append(interventionRows, interventionRow{
			RunID:     r.ID,
			TaskID:    r.TaskID,
			TaskTitle: r.TaskTitle,
			Message:   r.Feedback,
			CreatedAt: r.CreatedAt.String(),
		})
	}

	usageTotal, err := h.q.SumUsageTotal(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	usageByProvider, err := h.q.SumUsageByProvider(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	providerRows := make([]providerCostRow, 0, len(usageByProvider))
	for _, u := range usageByProvider {
		providerRows = append(providerRows, providerCostRow{
			Provider:     u.Provider,
			InputTokens:  u.InputTokens,
			OutputTokens: u.OutputTokens,
			CostUSD:      u.CostUsd,
			RunCount:     u.RunCount,
		})
	}

	agentConfigStats, err := h.agentConfigStats(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	usageByDay, err := h.q.SumUsageByDay(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	dayRows := make([]costByDayRow, 0, len(usageByDay))
	for _, d := range usageByDay {
		day, _ := d.Day.(string)
		dayRows = append(dayRows, costByDayRow{
			Day:          day,
			InputTokens:  d.InputTokens,
			OutputTokens: d.OutputTokens,
			CostUSD:      d.CostUsd,
			RunCount:     d.RunCount,
		})
	}

	taskCosts, err := h.q.SumUsageByTask(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	const topTasksByCost = 20
	topN := taskCosts
	if len(topN) > topTasksByCost {
		topN = topN[:topTasksByCost]
	}

	// Titles for just the top-N-by-cost rows, rather than pulling every
	// column of every task row via ListTasks.
	topIDs := make([]string, len(topN))
	for i, tc := range topN {
		topIDs[i] = tc.TaskID
	}
	titleRows, err := h.q.ListTaskTitlesByIDs(ctx, topIDs)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	titleByID := make(map[string]string, len(titleRows))
	for _, t := range titleRows {
		titleByID[t.ID] = t.Title
	}

	taskCostRows := make([]taskCostRow, 0, len(topN))
	for _, tc := range topN {
		taskCostRows = append(taskCostRows, taskCostRow{
			TaskID:       tc.TaskID,
			TaskTitle:    titleByID[tc.TaskID],
			InputTokens:  tc.InputTokens,
			OutputTokens: tc.OutputTokens,
			CostUSD:      tc.CostUsd,
		})
	}

	repoConcurrency, err := h.repoConcurrency(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	repoCosts, err := h.q.SumUsageByRepo(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	repoNameByID := make(map[string]string, len(repoCosts))
	if len(repoCosts) > 0 {
		repos, err := h.q.ListRepos(ctx)
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, repo := range repos {
			repoNameByID[repo.ID] = repo.Name
		}
	}
	repoCostRows := make([]repoCostRow, 0, len(repoCosts))
	for _, rc := range repoCosts {
		repoCostRows = append(repoCostRows, repoCostRow{
			RepoID:       rc.RepoID,
			RepoName:     repoNameByID[rc.RepoID],
			InputTokens:  rc.InputTokens,
			OutputTokens: rc.OutputTokens,
			CostUSD:      rc.CostUsd,
			RunCount:     rc.RunCount,
		})
	}

	JSON(w, http.StatusOK, dashboardResponse{
		LabelCounts:       counts,
		ActiveAgents:      activeRows,
		InterventionQueue: interventionRows,
		CostTotal: costTotals{
			InputTokens:  usageTotal.InputTokens,
			OutputTokens: usageTotal.OutputTokens,
			CostUSD:      usageTotal.CostUsd,
		},
		CostByProvider:   providerRows,
		AgentConfigStats: agentConfigStats,
		CostByDay:        dayRows,
		CostByTask:       taskCostRows,
		CostByRepo:       repoCostRows,
		ClaudeUsage:      h.claudeUsage(ctx),
		RepoConcurrency:  repoConcurrency,
		GlobalCostBudget: h.globalCostBudget(dayRows),
	})
}

// globalCostBudget renders the dispatcher's global spend-ceiling snapshot
// (see GlobalCostReporter) plus a simple burn-rate forecast, or nil if no
// dispatcher was wired or neither daily/monthly cap is configured — a
// forecast against an unlimited budget has nothing to be "on track" toward.
// dayRows is the same CostByDay series already fetched for the dashboard
// response (most-recent-day-first, see SumUsageByDay), reused here instead
// of a second query.
func (h *DashboardHandler) globalCostBudget(dayRows []costByDayRow) *globalCostBudgetView {
	if h.globalCost == nil {
		return nil
	}
	status := h.globalCost.GlobalCostStatus()
	if status.DailyLimitUSD <= 0 && status.MonthlyLimitUSD <= 0 {
		return nil
	}

	view := &globalCostBudgetView{GlobalCostStatus: status}

	// Trailing 7-day mean of recorded daily cost (dayRows is most-recent-
	// first and capped at 30 days by SumUsageByDay), extrapolated linearly to
	// the end of the current UTC day/month — deliberately simple; the value
	// is "am I on track to blow through this", not an accurate prediction.
	const trailingDays = 7
	n := len(dayRows)
	if n > trailingDays {
		n = trailingDays
	}
	var sum float64
	for i := 0; i < n; i++ {
		sum += dayRows[i].CostUSD
	}
	if n == 0 {
		return view
	}
	dailyMean := sum / float64(n)

	now := time.Now().UTC()
	if status.DailyLimitUSD > 0 {
		// The rest of "today" plus today's own already-recorded spend —
		// forecasting the day's total, not just what's left, so it compares
		// directly against DailyLimitUSD/DailySpentUSD.
		forecast := status.DailySpentUSD + dailyMean*hoursRemainingInDay(now)/24
		view.DailyForecastUSD = &forecast
	}
	if status.MonthlyLimitUSD > 0 {
		daysRemaining := float64(daysRemainingInMonth(now))
		forecast := status.MonthlySpentUSD + dailyMean*daysRemaining
		view.MonthlyForecastUSD = &forecast
	}
	return view
}

// hoursRemainingInDay returns how many hours remain in now's UTC calendar
// day (including the fractional current hour), used to extrapolate the
// trailing daily mean to a same-day total.
func hoursRemainingInDay(now time.Time) float64 {
	startOfTomorrow := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	return startOfTomorrow.Sub(now).Hours()
}

// daysRemainingInMonth returns how many days (including today) remain in
// now's UTC calendar month, used to extrapolate the trailing daily mean to a
// month-to-date total.
func daysRemainingInMonth(now time.Time) int {
	firstOfNextMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
	lastOfMonth := firstOfNextMonth.AddDate(0, 0, -1)
	return lastOfMonth.Day() - now.Day() + 1
}

// repoConcurrency builds the per-repo worker-slot breakdown (see
// repoConcurrencyRow) from the same in-flight-run signal the dispatcher uses
// to enforce repos.max_concurrent_runs (CountActiveRunsByRepo). Only repos
// with at least one in-flight run are returned, sorted by in-use descending
// so the busiest repos surface first.
func (h *DashboardHandler) repoConcurrency(ctx context.Context) ([]repoConcurrencyRow, error) {
	counts, err := h.q.CountActiveRunsByRepo(ctx)
	if err != nil {
		return nil, err
	}
	if len(counts) == 0 {
		return []repoConcurrencyRow{}, nil
	}

	rows := make([]repoConcurrencyRow, 0, len(counts))
	for _, c := range counts {
		repo, err := h.q.GetRepo(ctx, c.RepoID)
		if err != nil {
			// A repo can be deleted out from under an in-flight run's row
			// (repo_id has no FK enforcement here); skip rather than fail
			// the whole dashboard request.
			continue
		}
		limit := h.maxWorkers
		if repo.MaxConcurrentRuns != nil && *repo.MaxConcurrentRuns > 0 {
			limit = int(*repo.MaxConcurrentRuns)
		}
		rows = append(rows, repoConcurrencyRow{
			RepoID:   repo.ID,
			RepoName: repo.Name,
			InUse:    c.InUse,
			Limit:    limit,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].InUse > rows[j].InUse })
	return rows, nil
}

// CostByTask returns the full per-task cost rollup (no top-N cap, no
// titles) as a lightweight { task_id, cost_usd } map source for the board
// page's "cost of the currently-selected filter" badge, which needs cost
// for every visible task, not just the top-N-by-cost the dashboard shows.
func (h *DashboardHandler) CostByTask(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.SumUsageByTask(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]taskCostRow, 0, len(rows))
	for _, tc := range rows {
		out = append(out, taskCostRow{
			TaskID:       tc.TaskID,
			InputTokens:  tc.InputTokens,
			OutputTokens: tc.OutputTokens,
			CostUSD:      tc.CostUsd,
		})
	}
	JSON(w, http.StatusOK, out)
}

// agentConfigStats builds the per-agent-config analytics table by combining
// four queries:
//   - RunStatsByAgentConfig: run outcome counts, avg duration, tokens/cost.
//   - ListRunDurationsByAgentConfig: raw per-run durations, used here to
//     compute p90 duration per config (SQLite has no percentile aggregate).
//   - ListTaskLastAgentConfig: per-task last-run agent config and the
//     task's live transient_retry_count snapshot.
//   - ListTaskRunCountsByAgentConfig: per-(task, config) run counts, used to
//     proportionally split each done task's "task credit" across every
//     config that contributed to it ("runs per task").
//
// See agentConfigStatRow's doc comment for the two attribution/semantic
// caveats (proportional split for AvgRunsPerTask vs. last-run attribution for
// the retry fields; live/resettable retry snapshot) that apply to
// AvgRunsPerTask, AvgTransientRetries, and TasksWithRetries.
func (h *DashboardHandler) agentConfigStats(ctx context.Context) ([]agentConfigStatRow, error) {
	stats, err := h.q.RunStatsByAgentConfig(ctx)
	if err != nil {
		return nil, err
	}
	if len(stats) == 0 {
		return []agentConfigStatRow{}, nil
	}

	rows := make([]agentConfigStatRow, 0, len(stats))
	byConfig := make(map[string]*agentConfigStatRow, len(stats))
	for _, s := range stats {
		row := agentConfigStatRow{
			AgentConfigID:     s.AgentConfigID,
			AgentName:         s.AgentName,
			Provider:          s.Provider,
			RunCount:          s.RunCount,
			CompletedCount:    s.CompletedCount,
			FailedCount:       s.FailedCount,
			WaitingHumanCount: s.WaitingHumanCount,
			AvgDurationSecs:   s.AvgDurationSecs,
			AvgTurnsUsed:      s.AvgTurnsUsed,
			MaxTurns:          s.MaxTurns,
			InputTokens:       s.InputTokens,
			OutputTokens:      s.OutputTokens,
			CostUSD:           s.CostUsd,
		}
		if s.RunCount > 0 {
			row.SuccessRatePercent = float64(s.CompletedCount) / float64(s.RunCount) * 100
		}
		rows = append(rows, row)
	}
	for i := range rows {
		byConfig[rows[i].AgentConfigID] = &rows[i]
	}

	// p90 duration per agent config: durations arrive pre-sorted ascending
	// per agent_config_id (see ListRunDurationsByAgentConfig's ORDER BY), so
	// a single pass grouping consecutive rows is enough — no need to
	// re-sort in Go.
	durations, err := h.q.ListRunDurationsByAgentConfig(ctx)
	if err != nil {
		return nil, err
	}
	durationsByConfig := make(map[string][]float64)
	for _, d := range durations {
		if d.AgentConfigID == nil {
			continue
		}
		durationsByConfig[*d.AgentConfigID] = append(durationsByConfig[*d.AgentConfigID], d.DurationSecs)
	}
	for id, ds := range durationsByConfig {
		row, ok := byConfig[id]
		if !ok {
			continue
		}
		row.P90DurationSecs = percentile90(ds)
	}

	// p90 turns per agent config: same single-pass grouping as durations
	// above (rows arrive pre-sorted ascending per agent_config_id). Only runs
	// that actually reported a turn count are in this set — providers that
	// report none are absent rather than counted as zero.
	turns, err := h.q.ListRunTurnsByAgentConfig(ctx)
	if err != nil {
		return nil, err
	}
	turnsByConfig := make(map[string][]float64)
	for _, t := range turns {
		if t.AgentConfigID == nil {
			continue
		}
		turnsByConfig[*t.AgentConfigID] = append(turnsByConfig[*t.AgentConfigID], float64(t.TurnsUsed))
	}
	for id, ts := range turnsByConfig {
		row, ok := byConfig[id]
		if !ok {
			continue
		}
		row.P90TurnsUsed = percentile90(ts)
	}

	// Retry snapshot: attribute each done task entirely to the agent config
	// of its last run (see agentConfigStatRow doc comment).
	taskConfigs, err := h.q.ListTaskLastAgentConfig(ctx)
	if err != nil {
		return nil, err
	}
	type retryAcc struct {
		totalRetries   int64
		taskCount      int64
		tasksWithRetry int64
	}
	retriesByConfig := make(map[string]*retryAcc)
	for _, tc := range taskConfigs {
		if tc.LastAgentConfigID == nil {
			continue
		}
		acc, ok := retriesByConfig[*tc.LastAgentConfigID]
		if !ok {
			acc = &retryAcc{}
			retriesByConfig[*tc.LastAgentConfigID] = acc
		}
		acc.totalRetries += tc.TransientRetryCount
		acc.taskCount++
		if tc.TransientRetryCount > 0 {
			acc.tasksWithRetry++
		}
	}
	for id, acc := range retriesByConfig {
		row, ok := byConfig[id]
		if !ok || acc.taskCount == 0 {
			continue
		}
		row.AvgTransientRetries = float64(acc.totalRetries) / float64(acc.taskCount)
		row.TasksWithRetries = acc.tasksWithRetry
	}

	// Runs-per-task: split each done task's "task credit" proportionally
	// across every agent config that contributed a run to it, weighted by
	// that config's share of the task's total runs (see agentConfigStatRow
	// doc comment). Requires two passes over ListTaskRunCountsByAgentConfig:
	// first to total each task's run count, then to distribute credit.
	taskRunCounts, err := h.q.ListTaskRunCountsByAgentConfig(ctx)
	if err != nil {
		return nil, err
	}
	totalRunsByTask := make(map[string]int64, len(taskRunCounts))
	for _, tr := range taskRunCounts {
		if tr.AgentConfigID == nil {
			continue
		}
		totalRunsByTask[tr.TaskID] += tr.RunCount
	}
	type runsAcc struct {
		totalRuns   float64 // sum of this config's run counts across tasks it touched
		taskCredits float64 // sum of this config's proportional share of each task
	}
	runsByConfig := make(map[string]*runsAcc)
	for _, tr := range taskRunCounts {
		if tr.AgentConfigID == nil {
			continue
		}
		totalForTask := totalRunsByTask[tr.TaskID]
		if totalForTask == 0 {
			continue
		}
		acc, ok := runsByConfig[*tr.AgentConfigID]
		if !ok {
			acc = &runsAcc{}
			runsByConfig[*tr.AgentConfigID] = acc
		}
		acc.totalRuns += float64(tr.RunCount)
		acc.taskCredits += float64(tr.RunCount) / float64(totalForTask)
	}
	for id, acc := range runsByConfig {
		row, ok := byConfig[id]
		if !ok || acc.taskCredits == 0 {
			continue
		}
		row.AvgRunsPerTask = acc.totalRuns / acc.taskCredits
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].RunCount > rows[j].RunCount })
	return rows, nil
}

// percentile90 returns the 90th-percentile value from a slice already sorted
// ascending, using nearest-rank interpolation. Returns 0 for an empty slice.
func percentile90(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	// Nearest-rank: index = ceil(0.9 * n) - 1, clamped to the last element.
	idx := int(math.Ceil(0.9*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx]
}

// claudeUsage returns the current Claude account's rate-limit utilization,
// using a short-TTL cache to avoid hitting Anthropic's usage endpoint on
// every dashboard refresh (the dashboard page refetches on several WS
// events). Never fails — degrades to Available: false on any error,
// including missing credentials. If the last fetch was rate-limited by
// Anthropic (429), the cache is held for claudeUsageRateLimitedCacheTTL
// instead of the normal claudeUsageCacheTTL to back off from the
// already-rate-limited endpoint.
func (h *DashboardHandler) claudeUsage(ctx context.Context) claudeUsageResponse {
	h.usageMu.Lock()
	ttl := claudeUsageCacheTTL
	if h.cachedUsage.RateLimited {
		ttl = claudeUsageRateLimitedCacheTTL
	}
	if time.Since(h.cachedUsageAt) < ttl {
		cached := h.cachedUsage
		h.usageMu.Unlock()
		return cached
	}
	h.usageMu.Unlock()

	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	usage, err := providers.FetchClaudeUsage(fetchCtx)
	var result claudeUsageResponse
	if err != nil {
		if !errors.Is(err, providers.ErrNoClaudeCredentials) {
			slog.Debug("dashboard: claude usage fetch failed", "err", err)
		}
		if errors.Is(err, providers.ErrClaudeUsageRateLimited) {
			result = claudeUsageResponse{Available: false, RateLimited: true}
		} else {
			result = claudeUsageResponse{Available: false}
		}
	} else {
		result = claudeUsageResponse{
			Available:        true,
			FiveHourPercent:  usage.FiveHourPercent,
			FiveHourResetsAt: usage.FiveHourResetsAt,
			WeeklyPercent:    usage.WeeklyPercent,
			WeeklyResetsAt:   usage.WeeklyResetsAt,
		}
	}

	h.usageMu.Lock()
	h.cachedUsage = result
	h.cachedUsageAt = time.Now()
	h.usageMu.Unlock()

	return result
}
