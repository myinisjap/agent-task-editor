package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/needsme"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// ReviewBriefHandler serves GET /tasks/{id}/review-brief.
type ReviewBriefHandler struct {
	q     *gen.Queries
	cache *reviewbrief.Cache
}

// NewReviewBriefHandler builds a ReviewBriefHandler.
func NewReviewBriefHandler(q *gen.Queries, cache *reviewbrief.Cache) *ReviewBriefHandler {
	if cache == nil {
		cache = reviewbrief.NewCache()
	}
	return &ReviewBriefHandler{q: q, cache: cache}
}

type briefTopFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type briefChanged struct {
	Files     int            `json:"files"`
	Additions int            `json:"additions"`
	Deletions int            `json:"deletions"`
	TopFiles  []briefTopFile `json:"top_files"`
}

type briefResponse struct {
	HumanGate     bool                             `json:"human_gate"`
	Changed       briefChanged                     `json:"changed"`
	RiskFlags     []reviewbrief.Flag               `json:"risk_flags"`
	Verification  *reviewbrief.VerificationSummary `json:"verification"`
	Runs          reviewbrief.RunSummary           `json:"runs"`
	AgentSummary  *reviewbrief.AgentSummary        `json:"agent_summary"`
	DiffAvailable bool                             `json:"diff_available"`
	Truncated     bool                             `json:"truncated"`
	ComputedAt    time.Time                        `json:"computed_at"`
}

// Get returns the review brief for a task.
func (h *ReviewBriefHandler) Get(w http.ResponseWriter, r *http.Request) {
	task, err := h.q.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	b, err := h.cache.Build(r.Context(), h.q, task)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to build review brief")
		return
	}
	resp := briefResponse{
		Changed:   briefChanged{Files: b.Changed.Files, Additions: b.Changed.Additions, Deletions: b.Changed.Deletions, TopFiles: []briefTopFile{}},
		RiskFlags: b.Flags, Verification: b.Verification, Runs: b.Runs, AgentSummary: b.AgentSummary,
		DiffAvailable: b.DiffAvailable, Truncated: b.Truncated, ComputedAt: b.ComputedAt,
	}
	for _, f := range b.Changed.TopFiles {
		resp.Changed.TopFiles = append(resp.Changed.TopFiles, briefTopFile{Path: f.Path, Status: f.Status, Additions: f.Added, Deletions: f.Removed})
	}
	for i := range resp.RiskFlags {
		if resp.RiskFlags[i].Files == nil {
			resp.RiskFlags[i].Files = []string{}
		}
	}
	if labels, err := h.q.ListWorkflowLabels(r.Context(), task.WorkflowID); err == nil {
		if trans, err := h.q.ListWorkflowTransitions(r.Context(), task.WorkflowID); err == nil {
			resp.HumanGate = task.Archived == 0 && needsme.IsHumanGate(needsme.WorkflowDef{Labels: labels, Transitions: trans}, task.Label)
		}
	}
	JSON(w, http.StatusOK, resp)
}
