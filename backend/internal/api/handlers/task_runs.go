package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// RunCanceller signals an in-flight agent run to stop and reports whether the
// worker pool is currently saturated (no free slot). It is implemented by the
// agent pool; it may be nil in contexts (e.g. some tests) where no pool is
// wired, in which case CancelRun reports the run as no longer active and
// queuePositionMap treats the pool as never saturated (queue_position stays
// nil for every task, since there's no pool to actually queue against).
type RunCanceller interface {
	Cancel(runID string) bool
	// Saturated reports whether every worker slot is currently busy, i.e.
	// an eligible task would have to wait for one to free up.
	Saturated() bool
}

// ReplyDispatcher starts a new agent run carrying a human's answer to a
// waiting_human run's request_human question. Implemented by the agent
// dispatcher; may be nil in contexts (e.g. some tests) where no dispatcher is
// wired, in which case ReplyRun reports the feature unavailable.
type ReplyDispatcher interface {
	DispatchReply(ctx context.Context, taskID, message string) (string, error)
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

// CancelRun requests cancellation of an in-flight agent run. It only signals the
// pool's cancel registry (which cancels the run's context, propagating to CLI
// subprocesses and aborting HTTP providers); the pool marks the run "cancelled"
// and pauses the task asynchronously once the provider returns, then broadcasts
// task.agent_done. Returns 404 if the run doesn't belong to the task, 409 if the
// run isn't currently running, and 202 Accepted once cancellation is signalled.
func (h *TasksHandler) CancelRun(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	runID := chi.URLParam(r, "run_id")

	run, err := h.q.GetAgentRun(r.Context(), runID)
	if err != nil || run.TaskID != taskID {
		Err(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Status != "running" {
		Err(w, http.StatusConflict, "run is not running")
		return
	}
	if h.canceller == nil || !h.canceller.Cancel(runID) {
		// Not in the registry — it finished between the status read and here, or
		// is running on a different server instance.
		Err(w, http.StatusConflict, "run is no longer active")
		return
	}
	JSON(w, http.StatusAccepted, map[string]string{"status": "cancelling", "run_id": runID})
}

// ReplyRun answers a waiting_human run's request_human question with text and
// continues the work: it starts a new run that resumes the prior provider
// session where supported (claude), or starts cold with the reply injected
// into the prompt. The task stays on its current label; the replied-to run
// keeps its waiting_human status (matching the approve/reject flows). Returns
// 404 if the run doesn't belong to the task, 409 if it isn't the task's active
// waiting_human run, and 202 with the new run's id once dispatched.
func (h *TasksHandler) ReplyRun(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	runID := chi.URLParam(r, "run_id")

	var body struct {
		Message string `json:"message"`
	}
	if err := decode(r, &body); err != nil || strings.TrimSpace(body.Message) == "" {
		Err(w, http.StatusBadRequest, "message is required")
		return
	}

	run, err := h.q.GetAgentRun(r.Context(), runID)
	if err != nil || run.TaskID != taskID {
		Err(w, http.StatusNotFound, "run not found")
		return
	}
	task, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	if task.ActiveAgentRunID == nil || *task.ActiveAgentRunID != runID {
		Err(w, http.StatusConflict, "run is not the task's active run")
		return
	}
	if h.dispatcher == nil {
		Err(w, http.StatusServiceUnavailable, "reply dispatch is not available on this server")
		return
	}

	newRunID, err := h.dispatcher.DispatchReply(r.Context(), taskID, strings.TrimSpace(body.Message))
	switch {
	case err == nil:
		JSON(w, http.StatusAccepted, map[string]string{"run_id": newRunID})
	case errors.Is(err, agent.ErrRunNotWaiting), errors.Is(err, agent.ErrNoMatchingConfig):
		Err(w, http.StatusConflict, err.Error())
	case errors.Is(err, agent.ErrPoolSaturated):
		Err(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, sql.ErrNoRows):
		Err(w, http.StatusNotFound, "task not found")
	default:
		Err(w, http.StatusInternalServerError, err.Error())
	}
}

// GetRunLogs returns a page of a run's log entries in chronological order
// (oldest first), newest page first. Query parameters:
//   - limit: page size (default 200, capped at 1000)
//   - before: cursor (the id of the oldest entry the caller already has) —
//     returns entries older than it. Omit to get the most recent page (tail).
//
// The body is a plain JSON array. When older entries remain, X-Has-More is
// "true" and X-Prev-Cursor carries the id to pass as the next ?before= to load
// earlier.
func (h *TasksHandler) GetRunLogs(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	limit := parsePageLimit(r.URL.Query().Get("limit"), defaultLogPageLimit, maxLogPageLimit)

	// Fetch one extra row (newest first) to detect whether older entries exist.
	logs, err := h.q.ListAgentLogsPage(r.Context(), gen.ListAgentLogsPageParams{
		AgentRunID: runID,
		Column2:    r.URL.Query().Get("before"),
		Limit:      int64(limit) + 1,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(logs) > limit {
		logs = logs[:limit]
		// logs are newest-first; the oldest in this page is the last element.
		w.Header().Set("X-Has-More", "true")
		w.Header().Set("X-Prev-Cursor", logs[len(logs)-1].ID)
	}
	// Reverse to chronological (oldest-first) order for display.
	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}
	JSON(w, http.StatusOK, logs)
}
