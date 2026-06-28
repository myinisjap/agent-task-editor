package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

type TasksHandler struct {
	q      *gen.Queries
	engine *workflow.Engine
}

func NewTasksHandler(q *gen.Queries, engine *workflow.Engine) *TasksHandler {
	return &TasksHandler{q: q, engine: engine}
}

func (h *TasksHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	label := r.URL.Query().Get("label")

	var (
		tasks []gen.Task
		err   error
	)
	if label != "" {
		tasks, err = h.q.ListTasksByLabel(ctx, label)
	} else {
		tasks, err = h.q.ListTasks(ctx)
	}
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, tasks)
}

func (h *TasksHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Type        string `json:"type"`
		RepoID      string `json:"repo_id"`
		WorkflowID  string `json:"workflow_id"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Title == "" || body.RepoID == "" || body.WorkflowID == "" {
		Err(w, http.StatusBadRequest, "title, repo_id, and workflow_id are required")
		return
	}
	if body.Type == "" {
		body.Type = "feature"
	}

	task, err := h.q.CreateTask(r.Context(), gen.CreateTaskParams{
		ID:          uuid.NewString(),
		Title:       body.Title,
		Description: body.Description,
		Type:        body.Type,
		Label:       "not_ready",
		RepoID:      body.RepoID,
		WorkflowID:  body.WorkflowID,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, task)
}

func (h *TasksHandler) Get(w http.ResponseWriter, r *http.Request) {
	task, err := h.q.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	JSON(w, http.StatusOK, task)
}

func (h *TasksHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Type        string `json:"type"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	task, err := h.q.UpdateTask(r.Context(), gen.UpdateTaskParams{
		Title:       body.Title,
		Description: body.Description,
		Type:        body.Type,
		ID:          chi.URLParam(r, "id"),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, task)
}

func (h *TasksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.q.DeleteTask(r.Context(), chi.URLParam(r, "id")); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *TasksHandler) MoveLabel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ToLabel string `json:"to_label"`
		Note    string `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ToLabel == "" {
		Err(w, http.StatusBadRequest, "to_label is required")
		return
	}

	taskID := chi.URLParam(r, "id")
	if err := h.engine.Transition(r.Context(), taskID, body.ToLabel, workflow.TriggerHuman, "", body.Note); err != nil {
		handleTransitionError(w, err)
		return
	}
	updated, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to fetch updated task")
		return
	}
	JSON(w, http.StatusOK, updated)
}

func (h *TasksHandler) Approve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Note string `json:"note"`
	}
	_ = decode(r, &body) // optional body

	taskID := chi.URLParam(r, "id")
	task, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}

	// Approve follows the "success" human transition defined for the current label.
	target, err := h.humanPathTarget(r.Context(), task, "success")
	if err != nil {
		Err(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.engine.Transition(r.Context(), taskID, target, workflow.TriggerHuman, "", body.Note); err != nil {
		handleTransitionError(w, err)
		return
	}
	updated, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to fetch updated task")
		return
	}
	JSON(w, http.StatusOK, updated)
}

// humanPathTarget returns the destination label of the human transition with the
// given path (e.g. "success" or "failure") defined for the task's current label.
func (h *TasksHandler) humanPathTarget(ctx context.Context, task gen.Task, path string) (string, error) {
	transitions, err := h.q.ListWorkflowTransitions(ctx, task.WorkflowID)
	if err != nil {
		return "", fmt.Errorf("failed to load workflow transitions")
	}
	for _, t := range transitions {
		if t.FromLabel == task.Label && t.TriggerType == "human" && t.Path != nil && *t.Path == path {
			return t.ToLabel, nil
		}
	}
	return "", fmt.Errorf("no %q human transition defined from %q", path, task.Label)
}

func (h *TasksHandler) Reject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Note    string `json:"note"`
		ToLabel string `json:"to_label"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	taskID := chi.URLParam(r, "id")

	task, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}

	// Reject follows the "failure" human transition defined for the current label,
	// unless the caller supplies an explicit target.
	toLabel := body.ToLabel
	if toLabel == "" {
		toLabel, err = h.humanPathTarget(r.Context(), task, "failure")
		if err != nil {
			Err(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Persist the rejection note as feedback on the prior run so the next dispatch
	// injects it at the top of the agent's prompt.
	if body.Note != "" && task.CurrentAgentRunID != nil {
		if err := h.q.SetAgentRunFeedback(r.Context(), gen.SetAgentRunFeedbackParams{
			Feedback: &body.Note,
			ID:       *task.CurrentAgentRunID,
		}); err != nil {
			Err(w, http.StatusInternalServerError, "failed to save feedback")
			return
		}
	}

	if err := h.engine.Transition(r.Context(), taskID, toLabel, workflow.TriggerHuman, "", body.Note); err != nil {
		handleTransitionError(w, err)
		return
	}
	updated, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to fetch updated task")
		return
	}
	JSON(w, http.StatusOK, updated)
}

func (h *TasksHandler) UpdateNotes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Notes  string `json:"notes"`
		Append bool   `json:"append"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	taskID := chi.URLParam(r, "id")
	if body.Append {
		existing, err := h.q.GetTask(r.Context(), taskID)
		if err != nil {
			Err(w, http.StatusNotFound, "task not found")
			return
		}
		if existing.AgentNotes != "" {
			body.Notes = existing.AgentNotes + "\n\n" + body.Notes
		}
	}

	task, err := h.q.UpdateTaskNotes(r.Context(), gen.UpdateTaskNotesParams{
		AgentNotes: body.Notes,
		ID:         taskID,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, task)
}

func (h *TasksHandler) Rerun(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if _, err := h.q.GetTask(r.Context(), taskID); err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	if err := h.q.ClearActiveAgentRun(r.Context(), taskID); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *TasksHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := h.q.ListAgentRuns(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, runs)
}

func (h *TasksHandler) GetRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.q.GetAgentRun(r.Context(), chi.URLParam(r, "run_id"))
	if err != nil {
		Err(w, http.StatusNotFound, "run not found")
		return
	}
	JSON(w, http.StatusOK, run)
}

func (h *TasksHandler) GetRunLogs(w http.ResponseWriter, r *http.Request) {
	logs, err := h.q.ListAgentLogs(r.Context(), chi.URLParam(r, "run_id"))
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, logs)
}

func handleTransitionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workflow.ErrNoTransition):
		Err(w, http.StatusBadRequest, "no transition defined between these labels")
	case errors.Is(err, workflow.ErrGateRequired):
		Err(w, http.StatusForbidden, "transition requires human approval")
	case errors.Is(err, workflow.ErrAgentIgnored):
		Err(w, http.StatusForbidden, "destination label is ignored by agents")
	case errors.Is(err, workflow.ErrTaskNotFound):
		Err(w, http.StatusNotFound, "task not found")
	default:
		Err(w, http.StatusInternalServerError, err.Error())
	}
}
