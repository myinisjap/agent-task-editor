package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// AutonomyHandler serves shadow autonomy decisions.
type AutonomyHandler struct{ q *gen.Queries }

func NewAutonomyHandler(q *gen.Queries) *AutonomyHandler { return &AutonomyHandler{q: q} }

type autonomyDecisionResponse struct {
	ID         string            `json:"id"`
	TaskID     string            `json:"task_id"`
	WorkflowID string            `json:"workflow_id"`
	Label      string            `json:"label"`
	RunID      *string           `json:"run_id"`
	Mode       string            `json:"mode"`
	Decision   string            `json:"decision"`
	Reasons    []autonomy.Reason `json:"reasons"`
	CreatedAt  time.Time         `json:"created_at"`
}

// GetDecision returns the latest recorded decision for the task's current label.
func (h *AutonomyHandler) GetDecision(w http.ResponseWriter, r *http.Request) {
	task, err := h.q.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "task not found")
			return
		}
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	d, err := h.q.GetLatestAutonomyDecision(r.Context(), gen.GetLatestAutonomyDecisionParams{TaskID: task.ID, Label: task.Label})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "no autonomy decision for the task's current label")
			return
		}
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	reasons := []autonomy.Reason{}
	_ = json.Unmarshal([]byte(d.Reasons), &reasons)
	if reasons == nil {
		reasons = []autonomy.Reason{}
	}
	JSON(w, http.StatusOK, autonomyDecisionResponse{
		ID: d.ID, TaskID: d.TaskID, WorkflowID: d.WorkflowID, Label: d.Label, RunID: d.RunID,
		Mode: d.Mode, Decision: d.Decision, Reasons: reasons, CreatedAt: d.CreatedAt,
	})
}

// SetAlwaysAsk sets the per-task always_ask override (a hard stop for autonomy).
func (h *TasksHandler) SetAlwaysAsk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AlwaysAsk bool `json:"always_ask"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	v := int64(0)
	if body.AlwaysAsk {
		v = 1
	}
	id := chi.URLParam(r, "id")
	n, err := h.q.SetTaskAlwaysAsk(r.Context(), gen.SetTaskAlwaysAskParams{AlwaysAsk: v, ID: id})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	task, err := h.q.GetTask(r.Context(), id)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toTaskResponse(task))
}
