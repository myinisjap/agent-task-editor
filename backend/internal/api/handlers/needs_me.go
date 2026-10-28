package handlers

import (
	"database/sql"
	"errors"
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

// SetBriefCache enables decision context (risk flags, summary, diff size, cost, verify result, preview count) on human_gate items. Nil-safe.
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
	// Decision context: human_gate items only, best effort (omitted when the
	// review brief could not be built).
	Summary      *string         `json:"summary,omitempty"`
	Changed      *needsMeChanged `json:"changed,omitempty"`
	CostUsd      *float64        `json:"cost_usd,omitempty"`
	CostUnknown  bool            `json:"cost_unknown,omitempty"`
	Verification *string         `json:"verification,omitempty"`
	PreviewCount int             `json:"preview_count,omitempty"`
}

type needsMeChanged struct {
	Files     int `json:"files"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
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
			if d, ok := h.decisionContext(r, it.TaskID); ok {
				v.RiskFlags = d.RiskFlags
				if d.Summary != "" {
					v.Summary = &d.Summary
				}
				v.Changed = d.Changed
				v.CostUsd = &d.CostUsd
				v.CostUnknown = d.CostUnknown
				v.Verification = &d.Verification
				v.PreviewCount = d.PreviewCount
			}
		}
		out = append(out, v)
	}
	JSON(w, http.StatusOK, map[string]any{"items": out})
}

// decisionCtx is the review-brief-derived context for one human_gate item.
type decisionCtx struct {
	RiskFlags    []string
	Summary      string
	Changed      *needsMeChanged
	CostUsd      float64
	CostUnknown  bool
	Verification string
	PreviewCount int
}

// decisionContext builds the brief once (diff part cached) and derives the
// row context from it. ok=false on any problem: the list must never fail
// because of the brief.
func (h *NeedsMeHandler) decisionContext(r *http.Request, taskID string) (decisionCtx, bool) {
	log := middleware.LoggerFromContext(r.Context())
	task, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		log.Debug("needs-me context: get task", "task_id", taskID, "err", err)
		return decisionCtx{}, false
	}
	b, err := h.cache.Build(r.Context(), h.q, task)
	if err != nil {
		log.Debug("needs-me context: build brief", "task_id", taskID, "err", err)
		return decisionCtx{}, false
	}
	d := decisionCtx{CostUsd: b.Runs.CostUsd, CostUnknown: b.Runs.CostUnknown, Verification: "not_run"}
	if b.DiffAvailable {
		d.Changed = &needsMeChanged{Files: b.Changed.Files, Additions: b.Changed.Additions, Deletions: b.Changed.Deletions}
		for _, fl := range b.Flags {
			d.RiskFlags = append(d.RiskFlags, fl.Code)
		}
	}
	if b.Verification != nil && b.Verification.Status != "" {
		d.Verification = b.Verification.Status
	}
	if b.AgentSummary != nil {
		d.Summary = reviewbrief.OneLine(b.AgentSummary.Text, 160)
	}
	if c, err := h.q.GetLatestPreviewCapture(r.Context(), taskID); err == nil {
		if imgs, err := h.q.ListPreviewImagesByCapture(r.Context(), c.ID); err == nil {
			d.PreviewCount = len(imgs)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		log.Debug("needs-me context: preview capture", "task_id", taskID, "err", err)
	}
	return d, true
}
