package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/cronexpr"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// SchedulesHandler manages task_schedules: recurring instantiation of a
// task_template against a repo on a cron expression.
type SchedulesHandler struct {
	q *gen.Queries
}

func NewSchedulesHandler(q *gen.Queries) *SchedulesHandler {
	return &SchedulesHandler{q: q}
}

func (h *SchedulesHandler) List(w http.ResponseWriter, r *http.Request) {
	schedules, err := h.q.ListTaskSchedules(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if schedules == nil {
		schedules = []gen.TaskSchedule{}
	}
	JSON(w, http.StatusOK, schedules)
}

func (h *SchedulesHandler) Get(w http.ResponseWriter, r *http.Request) {
	sched, err := h.q.GetTaskSchedule(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "schedule not found")
		return
	}
	JSON(w, http.StatusOK, sched)
}

// scheduleBody is the create/update request payload.
type scheduleBody struct {
	TemplateID  string `json:"template_id"`
	RepoID      string `json:"repo_id"`
	CronExpr    string `json:"cron_expr"`
	TargetLabel string `json:"target_label"`
	Enabled     *bool  `json:"enabled"`
}

func (h *SchedulesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body scheduleBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.TemplateID == "" {
		Err(w, http.StatusBadRequest, "template_id is required")
		return
	}
	if body.RepoID == "" {
		Err(w, http.StatusBadRequest, "repo_id is required")
		return
	}
	if _, err := cronexpr.Parse(body.CronExpr); err != nil {
		Err(w, http.StatusBadRequest, "invalid cron_expr: "+err.Error())
		return
	}
	if _, err := h.q.GetTaskTemplate(r.Context(), body.TemplateID); err != nil {
		Err(w, http.StatusNotFound, "template not found")
		return
	}
	if _, err := h.q.GetRepo(r.Context(), body.RepoID); err != nil {
		Err(w, http.StatusNotFound, "repo not found")
		return
	}
	if body.TargetLabel == "" {
		body.TargetLabel = "not_ready"
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	sched, err := h.q.CreateTaskSchedule(r.Context(), gen.CreateTaskScheduleParams{
		ID:          uuid.NewString(),
		TemplateID:  body.TemplateID,
		RepoID:      body.RepoID,
		CronExpr:    body.CronExpr,
		TargetLabel: body.TargetLabel,
		Enabled:     enabled,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, sched)
}

func (h *SchedulesHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body scheduleBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, err := cronexpr.Parse(body.CronExpr); err != nil {
		Err(w, http.StatusBadRequest, "invalid cron_expr: "+err.Error())
		return
	}
	if body.TargetLabel == "" {
		body.TargetLabel = "not_ready"
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	sched, err := h.q.UpdateTaskSchedule(r.Context(), gen.UpdateTaskScheduleParams{
		CronExpr:    body.CronExpr,
		TargetLabel: body.TargetLabel,
		Enabled:     enabled,
		ID:          chi.URLParam(r, "id"),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "schedule not found")
			return
		}
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, sched)
}

func (h *SchedulesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.q.DeleteTaskSchedule(r.Context(), chi.URLParam(r, "id")); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
