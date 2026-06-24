package handlers

import (
	"net/http"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type DashboardHandler struct {
	q *gen.Queries
}

func NewDashboardHandler(q *gen.Queries) *DashboardHandler {
	return &DashboardHandler{q: q}
}

type dashboardResponse struct {
	LabelCounts      map[string]int    `json:"label_counts"`
	ActiveAgents     []activeAgentRow  `json:"active_agents"`
	InterventionQueue []interventionRow `json:"intervention_queue"`
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

	JSON(w, http.StatusOK, dashboardResponse{
		LabelCounts:       counts,
		ActiveAgents:      activeRows,
		InterventionQueue: interventionRows,
	})
}
