package handlers

import (
	"net/http"

	"github.com/myinisjap/agent-task-editor/backend/internal/needsme"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// NeedsMeHandler serves GET /needs-me.
type NeedsMeHandler struct {
	q *gen.Queries
}

// NewNeedsMeHandler builds a NeedsMeHandler.
func NewNeedsMeHandler(q *gen.Queries) *NeedsMeHandler { return &NeedsMeHandler{q: q} }

type needsMeItem struct {
	TaskID     string  `json:"task_id"`
	TaskTitle  string  `json:"task_title"`
	RepoID     string  `json:"repo_id"`
	WorkflowID string  `json:"workflow_id"`
	Label      string  `json:"label"`
	Reason     string  `json:"reason"`
	Message    *string `json:"message,omitempty"`
	RunID      *string `json:"run_id,omitempty"`
	// EscalationReason is the waiting run's escalation code (see
	// agent_runs.escalation_reason); omitted when none was recorded.
	EscalationReason *string `json:"escalation_reason,omitempty"`
	Since            string  `json:"since"`
}

// List returns every task that needs a human, oldest first.
func (h *NeedsMeHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := needsme.Compute(r.Context(), h.q)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]needsMeItem, 0, len(items))
	for _, it := range items {
		v := needsMeItem{
			TaskID: it.TaskID, TaskTitle: it.TaskTitle, RepoID: it.RepoID, WorkflowID: it.WorkflowID, Label: it.Label,
			Reason: string(it.Reason), Since: it.Since.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if it.Message != "" {
			m := it.Message
			v.Message = &m
		}
		if it.RunID != "" {
			id := it.RunID
			v.RunID = &id
		}
		if it.EscalationReason != "" {
			er := it.EscalationReason
			v.EscalationReason = &er
		}
		out = append(out, v)
	}
	JSON(w, http.StatusOK, map[string]any{"items": out})
}
