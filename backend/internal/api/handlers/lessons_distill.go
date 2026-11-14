package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/lessons/distill"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// LessonDistillHandler serves the lesson distiller: start a run for a repo,
// list runs, and dismiss a proposed lesson. Accepting uses PATCH /lessons/{id}.
type LessonDistillHandler struct {
	q   *gen.Queries
	svc *distill.Service
}

func NewLessonDistillHandler(q *gen.Queries, svc *distill.Service) *LessonDistillHandler {
	return &LessonDistillHandler{q: q, svc: svc}
}

type lessonRunResponse struct {
	ID               string     `json:"id"`
	RepoID           string     `json:"repo_id"`
	Trigger          string     `json:"trigger"`
	Actor            string     `json:"actor"`
	Status           string     `json:"status"`
	ProviderConfigID *string    `json:"provider_config_id"`
	EvidenceCount    int64      `json:"evidence_count"`
	ProposalCount    int64      `json:"proposal_count"`
	Error            string     `json:"error"`
	CostUSD          float64    `json:"cost_usd"`
	InputTokens      int64      `json:"input_tokens"`
	OutputTokens     int64      `json:"output_tokens"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
}

func toLessonRunResponse(r gen.LessonDistillRun) lessonRunResponse {
	return lessonRunResponse{
		ID: r.ID, RepoID: r.RepoID, Trigger: r.Trigger, Actor: r.Actor, Status: r.Status,
		ProviderConfigID: r.ProviderConfigID, EvidenceCount: r.EvidenceCount, ProposalCount: r.ProposalCount,
		Error: r.Error, CostUSD: r.CostUsd, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
}

// StartRun starts a distiller run for a repo in the background.
//
// POST /api/v1/repos/{id}/lessons/distill
func (h *LessonDistillHandler) StartRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.svc.Start(r.Context(), chi.URLParam(r, "id"), middleware.ActorFromContext(r.Context()))
	switch {
	case err == nil:
		JSON(w, http.StatusAccepted, toLessonRunResponse(run))
	case errors.Is(err, distill.ErrNotFound):
		Err(w, http.StatusNotFound, "repo not found")
	case errors.Is(err, distill.ErrNoProvider), errors.Is(err, distill.ErrNoModel):
		Err(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, distill.ErrRunning):
		Err(w, http.StatusConflict, err.Error())
	default:
		Err(w, http.StatusInternalServerError, err.Error())
	}
}

// ListRuns returns a repo's recent distiller runs, newest first.
//
// GET /api/v1/repos/{id}/lessons/distill-runs?limit=
func (h *LessonDistillHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.q.GetRepo(r.Context(), id); err != nil {
		Err(w, http.StatusNotFound, "repo not found")
		return
	}
	limit := int64(10)
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 1 || n > 100 {
			Err(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	rows, err := h.q.ListLessonDistillRuns(r.Context(), gen.ListLessonDistillRunsParams{RepoID: id, Limit: limit})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]lessonRunResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toLessonRunResponse(row))
	}
	JSON(w, http.StatusOK, out)
}

// Dismiss removes a proposed distiller lesson and remembers it so the same
// lesson is not offered again.
//
// POST /api/v1/lessons/{id}/dismiss
func (h *LessonDistillHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Dismiss(r.Context(), chi.URLParam(r, "id"), middleware.ActorFromContext(r.Context()))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, distill.ErrNotFound):
		Err(w, http.StatusNotFound, "lesson not found")
	case errors.Is(err, distill.ErrNotProposal):
		Err(w, http.StatusConflict, err.Error())
	default:
		Err(w, http.StatusInternalServerError, err.Error())
	}
}
