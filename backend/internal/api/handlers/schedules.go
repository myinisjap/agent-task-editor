package handlers

import (
	"context"
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

// validateTargetLabelForRepo checks that label is one of the workflow labels
// configured for repo's workflow. A schedule's target_label is applied
// directly to the created task's label, so an unrecognized label would
// silently create a task the dispatcher/UI can't place anywhere in the
// workflow. ok is false (with no error) when the label simply isn't valid or
// the repo has no workflow assigned; err is non-nil only for lookup failures.
func (h *SchedulesHandler) validateTargetLabelForRepo(ctx context.Context, repo gen.Repo, label string) (ok bool, msg string, err error) {
	if repo.WorkflowID == nil || *repo.WorkflowID == "" {
		return false, "repo has no workflow assigned", nil
	}
	labels, err := h.q.ListWorkflowLabels(ctx, *repo.WorkflowID)
	if err != nil {
		return false, "", err
	}
	for _, l := range labels {
		if l.Name == label {
			return true, "", nil
		}
	}
	return false, "target_label is not a label in the repo's workflow", nil
}

// defaultTargetLabelForRepo returns the label a schedule targets when the
// request leaves target_label empty: the repo workflow's human-gate label (the
// lowest-sort_order agent_ignore label, falling back to the first label) so the
// created task waits for a human to promote it — "not_ready" for the default
// workflow, the equivalent gate for any custom one.
func (h *SchedulesHandler) defaultTargetLabelForRepo(ctx context.Context, repo gen.Repo) (string, error) {
	if repo.WorkflowID == nil || *repo.WorkflowID == "" {
		return "", nil
	}
	labels, err := h.q.ListWorkflowLabels(ctx, *repo.WorkflowID)
	if err != nil {
		return "", err
	}
	gate, first := gateLabel(labels)
	if gate != "" {
		return gate, nil
	}
	return first, nil
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
	repo, err := h.q.GetRepo(r.Context(), body.RepoID)
	if err != nil {
		Err(w, http.StatusNotFound, "repo not found")
		return
	}
	if body.TargetLabel == "" {
		if body.TargetLabel, err = h.defaultTargetLabelForRepo(r.Context(), repo); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if ok, msg, err := h.validateTargetLabelForRepo(r.Context(), repo, body.TargetLabel); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		Err(w, http.StatusBadRequest, msg)
		return
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
	// repo_id is immutable after creation (see CreateTaskSchedule), so the
	// workflow to validate target_label against is the existing schedule's repo.
	existing, err := h.q.GetTaskSchedule(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "schedule not found")
		return
	}
	repo, err := h.q.GetRepo(r.Context(), existing.RepoID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.TargetLabel == "" {
		if body.TargetLabel, err = h.defaultTargetLabelForRepo(r.Context(), repo); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if ok, msg, err := h.validateTargetLabelForRepo(r.Context(), repo, body.TargetLabel); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		Err(w, http.StatusBadRequest, msg)
		return
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
