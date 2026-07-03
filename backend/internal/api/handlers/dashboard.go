package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
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
	LabelCounts       map[string]int      `json:"label_counts"`
	ActiveAgents      []activeAgentRow    `json:"active_agents"`
	InterventionQueue []interventionRow   `json:"intervention_queue"`
	CostTotal         costTotals          `json:"cost_total"`
	CostByProvider    []providerCostRow   `json:"cost_by_provider"`
	ClaudeUsage       claudeUsageResponse `json:"claude_usage"`
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

	JSON(w, http.StatusOK, dashboardResponse{
		LabelCounts:       counts,
		ActiveAgents:      activeRows,
		InterventionQueue: interventionRows,
		CostTotal: costTotals{
			InputTokens:  usageTotal.InputTokens,
			OutputTokens: usageTotal.OutputTokens,
			CostUSD:      usageTotal.CostUsd,
		},
		CostByProvider: providerRows,
		ClaudeUsage:    h.claudeUsage(ctx),
	})
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
