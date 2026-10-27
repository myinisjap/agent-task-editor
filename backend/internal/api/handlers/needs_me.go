package handlers

import (
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"net/http"

	"github.com/myinisjap/agent-task-editor/backend/internal/needsme"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// NeedsMeHandler serves GET /needs-me.
type NeedsMeHandler struct {
	q     *gen.Queries
	cache *reviewbrief.Cache
}

// SetBriefCache enables risk_flags on human_gate items. Nil-safe.
func (h *NeedsMeHandler) SetBriefCache(c *reviewbrief.Cache) {
	if h != nil {
		h.cache = c
	}
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
	// RiskFlags are the review-brief flag codes; only set for human_gate items
	// whose branch diff could be computed.
	RiskFlags []string `json:"risk_flags,omitempty"`
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
		if h.cache != nil && it.Reason == needsme.ReasonHumanGate {
			v.RiskFlags = h.riskFlags(r, it.TaskID)
		}
		out = append(out, v)
	}
	JSON(w, http.StatusOK, map[string]any{"items": out})
}

// riskFlags returns the cached flag codes for a task, or nil on any problem
// (the list must never fail because of the brief).
func (h *NeedsMeHandler) riskFlags(r *http.Request, taskID string) []string {
	log := middleware.LoggerFromContext(r.Context())
	task, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		log.Debug("needs-me risk flags: get task", "task_id", taskID, "err", err)
		return nil
	}
	d, err := h.cache.Diff(r.Context(), h.q, task)
	if err != nil {
		log.Debug("needs-me risk flags: diff", "task_id", taskID, "err", err)
		return nil
	}
	if !d.Available || len(d.Flags) == 0 {
		return nil
	}
	codes := make([]string, 0, len(d.Flags))
	for _, fl := range d.Flags {
		codes = append(codes, fl.Code)
	}
	return codes
}
