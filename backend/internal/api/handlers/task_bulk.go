package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// SetPaused toggles whether a task is paused. A paused task is never picked
// up by the dispatcher (enforced at the SQL level in ListAgentPickupTasks),
// regardless of its current label. Pausing does not change the task's label
// and does not cancel any in-flight agent run; it only blocks future
// dispatch.
func (h *TasksHandler) SetPaused(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paused bool `json:"paused"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	pausedVal := int64(0)
	if body.Paused {
		pausedVal = 1
	}
	task, err := h.q.SetTaskPaused(r.Context(), gen.SetTaskPausedParams{
		Paused: pausedVal,
		ID:     chi.URLParam(r, "id"),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toTaskResponse(task))
}

// SetArchived toggles whether a task is archived. Archived tasks are hidden
// from the default board view (GET /tasks excludes them unless
// archived=all|only is passed), skipped by the ghsync PR-status sweep, and
// never dispatched to agents. Archiving does not change the task's label.
func (h *TasksHandler) SetArchived(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Archived bool `json:"archived"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	archivedVal := int64(0)
	if body.Archived {
		archivedVal = 1
	}
	task, err := h.q.SetTaskArchived(r.Context(), gen.SetTaskArchivedParams{
		Archived: archivedVal,
		ID:       chi.URLParam(r, "id"),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "task not found")
			return
		}
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toTaskResponse(task))
}

// bulkResult reports the outcome of a bulk action for one task.
type bulkResult struct {
	ID    string `json:"id"`
	Ok    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Bulk applies one action to a set of tasks. Actions: "move" (requires
// to_label; validated per-task through the workflow engine), "pause",
// "resume", "archive", "unarchive". Each task is processed independently —
// one failure doesn't abort the rest — and the response reports per-task
// results plus 200 if everything succeeded, 207 otherwise.
func (h *TasksHandler) Bulk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs     []string `json:"ids"`
		Action  string   `json:"action"`
		ToLabel string   `json:"to_label"`
		Note    string   `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.IDs) == 0 {
		Err(w, http.StatusBadRequest, "ids is required")
		return
	}
	switch body.Action {
	case "move":
		if body.ToLabel == "" {
			Err(w, http.StatusBadRequest, "to_label is required for the move action")
			return
		}
	case "pause", "resume", "archive", "unarchive":
	default:
		Err(w, http.StatusBadRequest, "action must be one of: move, pause, resume, archive, unarchive")
		return
	}

	ctx := r.Context()
	actor := middleware.ActorFromContext(ctx)
	results := make([]bulkResult, 0, len(body.IDs))
	allOk := true
	for _, id := range body.IDs {
		var err error
		switch body.Action {
		case "move":
			err = h.engine.Transition(ctx, id, body.ToLabel, workflow.TriggerHuman, actor, body.Note)
		case "pause", "resume":
			paused := int64(0)
			if body.Action == "pause" {
				paused = 1
			}
			_, err = h.q.SetTaskPaused(ctx, gen.SetTaskPausedParams{Paused: paused, ID: id})
		case "archive", "unarchive":
			archived := int64(0)
			if body.Action == "archive" {
				archived = 1
			}
			_, err = h.q.SetTaskArchived(ctx, gen.SetTaskArchivedParams{Archived: archived, ID: id})
		}
		res := bulkResult{ID: id, Ok: err == nil}
		if err != nil {
			allOk = false
			res.Error = bulkErrorMessage(err)
		}
		results = append(results, res)
	}

	status := http.StatusOK
	if !allOk {
		status = http.StatusMultiStatus
	}
	JSON(w, status, map[string]any{"results": results})
}

// bulkErrorMessage maps per-task bulk action errors to the same messages the
// single-task endpoints use.
func bulkErrorMessage(err error) string {
	switch {
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, workflow.ErrTaskNotFound):
		return "task not found"
	case errors.Is(err, workflow.ErrNoTransition):
		return "no transition defined between these labels"
	case errors.Is(err, workflow.ErrGateRequired):
		return "transition requires human approval"
	case errors.Is(err, workflow.ErrAgentIgnored):
		return "destination label is ignored by agents"
	case errors.Is(err, workflow.ErrStale):
		return "task label changed concurrently; refresh and retry"
	default:
		return err.Error()
	}
}
