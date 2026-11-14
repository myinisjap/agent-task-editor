package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/myinisjap/agent-task-editor/backend/internal/intakecheck"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// SetIntake wires the intake assistant (possible duplicates + verdict chip).
func (h *TasksHandler) SetIntake(svc *intakecheck.Service) { h.intake = svc }

// withIntake adds the computed possible duplicates and the stored verdict.
func (h *TasksHandler) withIntake(ctx context.Context, resp taskResponse, task gen.Task) taskResponse {
	if h.intake == nil {
		return resp
	}
	resp.PossibleDuplicates = h.intake.Duplicates(ctx, task)
	if v, err := h.intake.Store.Verdicts(ctx, []string{task.ID}); err == nil {
		resp.IntakeVerdict = v[task.ID]
	}
	return resp
}

func (h *TasksHandler) intakeVerdicts(ctx context.Context, ids []string) map[string]string {
	if h.intake == nil {
		return nil
	}
	v, _ := h.intake.Store.Verdicts(ctx, ids)
	return v
}

func (h *TasksHandler) withVerdict(resp taskResponse, m map[string]string) taskResponse {
	resp.IntakeVerdict = m[resp.ID]
	return resp
}

// IntakeCheckHandler serves the deterministic similar-task lookup, the
// assistant check and the assistant settings.
type IntakeCheckHandler struct {
	svc *intakecheck.Service
}

func NewIntakeCheckHandler(svc *intakecheck.Service) *IntakeCheckHandler {
	return &IntakeCheckHandler{svc: svc}
}

// Similar returns open tasks in a repo whose title looks like q. No model call.
//
// GET /api/v1/tasks/similar?repo_id=&q=
func (h *IntakeCheckHandler) Similar(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("repo_id")
	if repoID == "" {
		Err(w, http.StatusBadRequest, "repo_id is required")
		return
	}
	JSON(w, http.StatusOK, h.svc.Similar(r.Context(), repoID, r.URL.Query().Get("q")))
}

// Get returns the task's latest assistant check.
//
// GET /api/v1/tasks/{id}/intake-check
func (h *IntakeCheckHandler) Get(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Store.GetCheck(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, intakecheck.ErrNotFound) {
		Err(w, http.StatusNotFound, "no intake check for this task")
		return
	}
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, c)
}

// Start runs the assistant on a task in the background (advisory only).
//
// POST /api/v1/tasks/{id}/intake-check
func (h *IntakeCheckHandler) Start(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Start(r.Context(), chi.URLParam(r, "id"), intakecheck.TriggerManual)
	switch {
	case err == nil:
		JSON(w, http.StatusAccepted, c)
	case errors.Is(err, intakecheck.ErrNoTask):
		Err(w, http.StatusNotFound, "task not found")
	case errors.Is(err, intakecheck.ErrDisabled), errors.Is(err, intakecheck.ErrNoProvider), errors.Is(err, intakecheck.ErrNoModel):
		Err(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, intakecheck.ErrRunning):
		Err(w, http.StatusConflict, err.Error())
	default:
		Err(w, http.StatusInternalServerError, err.Error())
	}
}

// GetSettings returns the assistant settings (off by default).
//
// GET /api/v1/settings/intake-assistant
func (h *IntakeCheckHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.svc.Store.GetSettings(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, s)
}

// UpdateSettings stores the assistant settings.
//
// PUT /api/v1/settings/intake-assistant
func (h *IntakeCheckHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body intakecheck.Settings
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ProviderConfigID != nil && *body.ProviderConfigID == "" {
		body.ProviderConfigID = nil
	}
	if body.ProviderConfigID != nil {
		if _, err := h.svc.Q.GetProviderConfig(r.Context(), *body.ProviderConfigID); err != nil {
			Err(w, http.StatusBadRequest, "provider config not found")
			return
		}
	}
	if body.Enabled && body.ProviderConfigID == nil {
		Err(w, http.StatusBadRequest, "provider_config_id is required to enable the intake assistant")
		return
	}
	if err := h.svc.Store.PutSettings(r.Context(), body); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, body)
}
