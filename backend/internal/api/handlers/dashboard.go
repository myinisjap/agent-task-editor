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
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// claudeUsageCacheTTL bounds how often the dashboard endpoint will call out
// to Anthropic's OAuth usage endpoint. The dashboard page refetches on
// several WS events (task label/agent state changes), which can be
// frequent during active runs — without a cache this would hammer
// Anthropic's usage endpoint.
const claudeUsageCacheTTL = 45 * time.Second

type DashboardHandler struct {
	q *gen.Queries

	usageMu       sync.Mutex
	cachedUsage   claudeUsageResponse
	cachedUsageAt time.Time
}

func NewDashboardHandler(q *gen.Queries) *DashboardHandler {
	return &DashboardHandler{q: q}
}

type dashboardResponse struct {
	LabelCounts       map[string]int       `json:"label_counts"`
	ActiveAgents      []activeAgentRow     `json:"active_agents"`
	InterventionQueue []interventionRow    `json:"intervention_queue"`
	CostTotal         costTotals           `json:"cost_total"`
	CostByProvider    []providerCostRow    `json:"cost_by_provider"`
	AgentConfigStats  []agentConfigStatRow `json:"agent_config_stats"`
	CostByDay         []costByDayRow       `json:"cost_by_day"`
	CostByTask        []taskCostRow        `json:"cost_by_task"`
	ClaudeUsage       claudeUsageResponse  `json:"claude_usage"`
}

// claudeUsageResponse is the live Claude account rate-limit utilization
// (5-hour rolling window + weekly window) from Anthropic's OAuth usage
// endpoint. Available is false when the server has no Claude OAuth
// credentials (~/.claude/.credentials.json) or the fetch failed for any
// other reason — this must never fail the /dashboard request as a whole.
type claudeUsageResponse struct {
	Available        bool       `json:"available"`
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
// duration, turns-to-done, transient-retry frequency, and token/cost usage.
// It answers "which agent config is actually performing?" by combining
// agent_runs (status/duration/tokens/cost) with tasks.transient_retry_count.
//
// Two caveats apply and are surfaced in docs/api.md and docs/agents.md:
//  1. AvgTurnsToDone and the retry fields are attributed entirely to a
//     task's *last* run's agent config, not proportionally split across
//     every config a task passed through (e.g. if a task was retried under
//     agent A, then reassigned to agent B which finished it, all of that
//     task's turns/retries count only toward B).
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
	AvgTurnsToDone      float64 `json:"avg_turns_to_done"`
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

	tasks, err := h.q.ListTasks(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	counts := map[string]int{}
	for _, t := range tasks {
		// Archived tasks are hidden from the board, so keep the dashboard's
		// per-label counts consistent with what the board shows.
		if t.Archived != 0 {
			continue
		}
		counts[t.Label]++
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

	// Titles for the top-N-by-cost table, looked up from the already-fetched
	// tasks slice above rather than a second query/join.
	titleByID := make(map[string]string, len(tasks))
	for _, t := range tasks {
		titleByID[t.ID] = t.Title
	}
	taskCosts, err := h.q.SumUsageByTask(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	const topTasksByCost = 20
	taskCostRows := make([]taskCostRow, 0, min(len(taskCosts), topTasksByCost))
	for i, tc := range taskCosts {
		if i >= topTasksByCost {
			break
		}
		taskCostRows = append(taskCostRows, taskCostRow{
			TaskID:       tc.TaskID,
			TaskTitle:    titleByID[tc.TaskID],
			InputTokens:  tc.InputTokens,
			OutputTokens: tc.OutputTokens,
			CostUSD:      tc.CostUsd,
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
		ClaudeUsage:      h.claudeUsage(ctx),
	})
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
// three queries:
//   - RunStatsByAgentConfig: run outcome counts, avg duration, tokens/cost.
//   - ListRunDurationsByAgentConfig: raw per-run durations, used here to
//     compute p90 duration per config (SQLite has no percentile aggregate).
//   - ListTaskLastAgentConfig: per-task last-run agent config, run count
//     ("turns to done"), and the task's live transient_retry_count snapshot.
//
// See agentConfigStatRow's doc comment for the two attribution/semantic
// caveats (last-run attribution; live/resettable retry snapshot) that apply
// to AvgTurnsToDone, AvgTransientRetries, and TasksWithRetries.
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

	// Turns-to-done and retry snapshot: attribute each done task entirely to
	// the agent config of its last run (see agentConfigStatRow doc comment).
	taskConfigs, err := h.q.ListTaskLastAgentConfig(ctx)
	if err != nil {
		return nil, err
	}
	type turnsAcc struct {
		totalRuns      int64
		totalRetries   int64
		taskCount      int64
		tasksWithRetry int64
	}
	turnsByConfig := make(map[string]*turnsAcc)
	for _, tc := range taskConfigs {
		if tc.LastAgentConfigID == nil {
			continue
		}
		acc, ok := turnsByConfig[*tc.LastAgentConfigID]
		if !ok {
			acc = &turnsAcc{}
			turnsByConfig[*tc.LastAgentConfigID] = acc
		}
		acc.totalRuns += tc.RunCount
		acc.totalRetries += tc.TransientRetryCount
		acc.taskCount++
		if tc.TransientRetryCount > 0 {
			acc.tasksWithRetry++
		}
	}
	for id, acc := range turnsByConfig {
		row, ok := byConfig[id]
		if !ok || acc.taskCount == 0 {
			continue
		}
		row.AvgTurnsToDone = float64(acc.totalRuns) / float64(acc.taskCount)
		row.AvgTransientRetries = float64(acc.totalRetries) / float64(acc.taskCount)
		row.TasksWithRetries = acc.tasksWithRetry
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
// including missing credentials.
func (h *DashboardHandler) claudeUsage(ctx context.Context) claudeUsageResponse {
	h.usageMu.Lock()
	if time.Since(h.cachedUsageAt) < claudeUsageCacheTTL {
		cached := h.cachedUsage
		h.usageMu.Unlock()
		return cached
	}
	h.usageMu.Unlock()

	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	usage, err := agent.FetchClaudeUsage(fetchCtx)
	var result claudeUsageResponse
	if err != nil {
		if !errors.Is(err, agent.ErrNoClaudeCredentials) {
			slog.Debug("dashboard: claude usage fetch failed", "err", err)
		}
		result = claudeUsageResponse{Available: false}
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
