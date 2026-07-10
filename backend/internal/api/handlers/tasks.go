package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/writeback"
)

// Task priority levels. Stored as a plain INTEGER column (tasks.priority) so
// ListAgentPickupTasks can order by a simple numeric ORDER BY priority DESC.
// Higher values are dispatched first when there are more eligible tasks than
// free workers; priority never preempts an already-running task.
const (
	PriorityLow    = -1
	PriorityNormal = 0
	PriorityHigh   = 1
	PriorityUrgent = 2
)

// validPriority reports whether p is one of the known priority levels.
func validPriority(p int) bool {
	switch p {
	case PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent:
		return true
	default:
		return false
	}
}

// Pagination defaults and caps. List endpoints return at most a page at a time,
// cursored on (created_at|timestamp, id). Callers pass ?limit= (clamped to the
// max) and ?after=/?before= cursors; the response carries the next cursor in a
// header so the body shape stays a plain array.
const (
	defaultTaskPageLimit = 200
	maxTaskPageLimit     = 500
	defaultLogPageLimit  = 200
	maxLogPageLimit      = 1000
)

// parsePageLimit parses a ?limit= value, falling back to def when empty or
// invalid and clamping into [1, max].
func parsePageLimit(raw string, def, max int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

type TasksHandler struct {
	q          *gen.Queries
	engine     *workflow.Engine
	uploadDir  string
	canceller  RunCanceller
	dispatcher ReplyDispatcher
	wb         *writeback.Writeback
}

func NewTasksHandler(q *gen.Queries, engine *workflow.Engine, uploadDir string, canceller RunCanceller, dispatcher ReplyDispatcher) *TasksHandler {
	return &TasksHandler{q: q, engine: engine, uploadDir: uploadDir, canceller: canceller, dispatcher: dispatcher, wb: writeback.New(q)}
}

// SetWriteback overrides the handler's writeback instance. Exported only for
// tests in other packages that need to fake out the underlying `gh` calls
// (e.g. to assert CreatePR/GitHubStatus/UpdateGitState fire the right
// write-back hooks without shelling out to a real gh binary).
func (h *TasksHandler) SetWriteback(wb *writeback.Writeback) {
	h.wb = wb
}

// List returns a page of tasks, optionally narrowed by query parameters:
//   - q: case-insensitive substring match against title and description
//   - label, repo_id, type, git_state: exact-match filters
//   - archived: "" (default) hides archived tasks, "only" returns just
//     archived tasks, "all" returns everything
//   - limit: page size (default 200, capped at 500)
//   - after: cursor (the id of the last task from the previous page)
//
// The body is a plain JSON array (newest first). When more tasks remain, the
// id to pass as the next ?after= cursor is returned in the X-Next-Cursor header.
func (h *TasksHandler) List(w http.ResponseWriter, r *http.Request) {
	qp := r.URL.Query()
	archived := qp.Get("archived")
	switch archived {
	case "", "all", "only":
	default:
		Err(w, http.StatusBadRequest, `archived must be "all" or "only"`)
		return
	}

	limit := parsePageLimit(qp.Get("limit"), defaultTaskPageLimit, maxTaskPageLimit)

	// parent_id short-circuits to the direct children of a parent (one family at
	// a time), bypassing the cursor pagination since a parent's child set is
	// naturally bounded by the subtask cap.
	if parentID := qp.Get("parent_id"); parentID != "" {
		children, cerr := h.q.ListSubtasks(r.Context(), &parentID)
		if cerr != nil {
			Err(w, http.StatusInternalServerError, cerr.Error())
			return
		}
		counts := h.dependencyCountMap(r.Context())
		rollups := h.subtaskRollupMap(r.Context())
		positions := h.queuePositionMap(r.Context())
		resp := toTaskResponses(children)
		for i := range resp {
			resp[i] = applyQueuePosition(applyRollup(applyDepCounts(resp[i], counts), rollups), positions)
		}
		JSON(w, http.StatusOK, resp)
		return
	}

	// Fetch one extra row so we can tell whether another page exists without a
	// separate COUNT.
	tasks, err := h.q.SearchTasksPage(r.Context(), gen.SearchTasksPageParams{
		Column1: qp.Get("q"),
		Column2: qp.Get("label"),
		Column3: qp.Get("repo_id"),
		Column4: qp.Get("type"),
		Column5: qp.Get("git_state"),
		Column6: archived,
		Column7: qp.Get("after"),
		Limit:   int64(limit) + 1,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(tasks) > limit {
		tasks = tasks[:limit]
		w.Header().Set("X-Next-Cursor", tasks[len(tasks)-1].ID)
	}
	counts := h.dependencyCountMap(r.Context())
	rollups := h.subtaskRollupMap(r.Context())
	positions := h.queuePositionMap(r.Context())
	resp := toTaskResponses(tasks)
	for i := range resp {
		resp[i] = applyQueuePosition(applyRollup(applyDepCounts(resp[i], counts), rollups), positions)
	}
	JSON(w, http.StatusOK, resp)
}

func (h *TasksHandler) Create(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")

	var title, description, taskType, repoID, workflowID string
	var attachmentPaths []string

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Parse multipart form (max 50 MB total)
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			Err(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
			return
		}
		title = r.FormValue("title")
		description = r.FormValue("description")
		taskType = r.FormValue("type")
		repoID = r.FormValue("repo_id")
		workflowID = r.FormValue("workflow_id")

		priority := PriorityNormal
		if raw := r.FormValue("priority"); raw != "" {
			p, perr := strconv.Atoi(raw)
			if perr != nil || !validPriority(p) {
				Err(w, http.StatusBadRequest, "priority must be one of -1 (low), 0 (normal), 1 (high), 2 (urgent)")
				return
			}
			priority = p
		}

		// We need a task ID before saving files
		taskID := uuid.NewString()

		// Handle uploaded image files
		paths, ok := h.saveUploadedAttachments(w, r, taskID)
		if !ok {
			return
		}
		attachmentPaths = paths

		// Marshal attachments to JSON
		attachmentsJSON, err := json.Marshal(attachmentPaths)
		if err != nil {
			Err(w, http.StatusInternalServerError, "failed to marshal attachments")
			return
		}

		if title == "" || repoID == "" || workflowID == "" {
			Err(w, http.StatusBadRequest, "title, repo_id, and workflow_id are required")
			return
		}
		if taskType == "" {
			taskType = "feature"
		}

		task, err := h.q.CreateTask(r.Context(), gen.CreateTaskParams{
			ID:          taskID,
			Title:       title,
			Description: description,
			Type:        taskType,
			Label:       "not_ready",
			RepoID:      repoID,
			WorkflowID:  workflowID,
			Attachments: string(attachmentsJSON),
			Priority:    int64(priority),
		})
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		JSON(w, http.StatusCreated, toTaskResponse(task))
		return
	}

	// Fallback: JSON body (no attachments)
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Type        string `json:"type"`
		RepoID      string `json:"repo_id"`
		WorkflowID  string `json:"workflow_id"`
		Priority    int    `json:"priority"`
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
	if !validPriority(body.Priority) {
		Err(w, http.StatusBadRequest, "priority must be one of -1 (low), 0 (normal), 1 (high), 2 (urgent)")
		return
	}

	task, err := h.q.CreateTask(r.Context(), gen.CreateTaskParams{
		ID:          uuid.NewString(),
		Title:       body.Title,
		Description: body.Description,
		Type:        body.Type,
		Label:       "not_ready",
		RepoID:      body.RepoID,
		WorkflowID:  body.WorkflowID,
		Attachments: "[]",
		Priority:    int64(body.Priority),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, toTaskResponse(task))
}

func (h *TasksHandler) Get(w http.ResponseWriter, r *http.Request) {
	task, err := h.q.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	resp := applyDepCounts(toTaskResponse(task), h.dependencyCountMap(r.Context()))
	resp = applyRollup(resp, h.subtaskRollupMap(r.Context()))
	resp = applyQueuePosition(resp, h.queuePositionMap(r.Context()))
	JSON(w, http.StatusOK, resp)
}

func (h *TasksHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Type        string   `json:"type"`
		RepoID      string   `json:"repo_id"`
		MaxCostUsd  *float64 `json:"max_cost_usd"`
		Priority    *int     `json:"priority"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.MaxCostUsd != nil && *body.MaxCostUsd < 0 {
		Err(w, http.StatusBadRequest, "max_cost_usd must be >= 0")
		return
	}
	if body.Priority != nil && !validPriority(*body.Priority) {
		Err(w, http.StatusBadRequest, "priority must be one of -1 (low), 0 (normal), 1 (high), 2 (urgent)")
		return
	}

	taskID := chi.URLParam(r, "id")

	// Fetch the existing task so we can preserve the repo_id/max_cost_usd/priority if
	// the caller didn't supply new values.
	existing, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}

	repoID := existing.RepoID
	if body.RepoID != "" {
		// Validate the supplied repo actually exists.
		if _, rerr := h.q.GetRepo(r.Context(), body.RepoID); rerr != nil {
			Err(w, http.StatusBadRequest, "repo not found")
			return
		}
		repoID = body.RepoID
	}

	maxCostUsd := existing.MaxCostUsd
	if body.MaxCostUsd != nil {
		maxCostUsd = *body.MaxCostUsd
	}

	priority := existing.Priority
	if body.Priority != nil {
		priority = int64(*body.Priority)
	}

	task, err := h.q.UpdateTask(r.Context(), gen.UpdateTaskParams{
		Title:       body.Title,
		Description: body.Description,
		Type:        body.Type,
		RepoID:      repoID,
		MaxCostUsd:  maxCostUsd,
		Priority:    priority,
		ID:          taskID,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toTaskResponse(task))
}

func (h *TasksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	logger := middleware.LoggerFromContext(r.Context()).With("task_id", taskID)
	// Best-effort: tear down the task's worktree before deleting the row. The
	// branch is kept for review. Look up the repo path for the worktree-remove.
	if task, err := h.q.GetTask(r.Context(), taskID); err == nil {
		if task.WorktreePath != "" {
			if repo, rerr := h.q.GetRepo(r.Context(), task.RepoID); rerr == nil {
				if out, gerr := exec.CommandContext(r.Context(), "git", "-C", repo.Path, "worktree", "remove", "--force", task.WorktreePath).CombinedOutput(); gerr != nil {
					logger.Warn("delete task: remove worktree", "err", gerr, "out", strings.TrimSpace(string(out)))
				}
			}
		}
		// Best-effort: remove uploaded attachments for this task.
		if h.uploadDir != "" {
			taskUploadDir := filepath.Join(h.uploadDir, taskID)
			if err := os.RemoveAll(taskUploadDir); err != nil {
				logger.Warn("delete task: remove uploads", "err", err)
			}
		}
	}
	if err := h.q.DeleteTask(r.Context(), taskID); err != nil {
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
	if err := h.engine.Transition(r.Context(), taskID, body.ToLabel, workflow.TriggerHuman, middleware.ActorFromContext(r.Context()), body.Note); err != nil {
		handleTransitionError(w, err)
		return
	}
	updated, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to fetch updated task")
		return
	}
	JSON(w, http.StatusOK, toTaskResponse(updated))
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

	if err := h.engine.Transition(r.Context(), taskID, target, workflow.TriggerHuman, middleware.ActorFromContext(r.Context()), body.Note); err != nil {
		handleTransitionError(w, err)
		return
	}
	updated, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to fetch updated task")
		return
	}
	JSON(w, http.StatusOK, toTaskResponse(updated))
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

	if err := h.engine.Transition(r.Context(), taskID, toLabel, workflow.TriggerHuman, middleware.ActorFromContext(r.Context()), body.Note); err != nil {
		handleTransitionError(w, err)
		return
	}
	updated, err := h.q.GetTask(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to fetch updated task")
		return
	}
	JSON(w, http.StatusOK, toTaskResponse(updated))
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
	JSON(w, http.StatusOK, toTaskResponse(task))
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

// ListLabelHistory returns the task's label-transition audit trail, oldest
// first. actor_id is the resolved named-token actor for human-triggered
// transitions (see middleware.ActorFromContext), or the agent run ID for
// agent-triggered transitions; it may be null/empty for anonymous/legacy
// single-token auth or system-triggered transitions.
func (h *TasksHandler) ListLabelHistory(w http.ResponseWriter, r *http.Request) {
	history, err := h.q.ListTaskLabelHistory(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, history)
}

func handleTransitionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workflow.ErrNoTransition):
		Err(w, http.StatusBadRequest, "no transition defined between these labels")
	case errors.Is(err, workflow.ErrGateRequired):
		Err(w, http.StatusForbidden, "transition requires human approval")
	case errors.Is(err, workflow.ErrAgentIgnored):
		Err(w, http.StatusForbidden, "destination label is ignored by agents")
	case errors.Is(err, workflow.ErrStale):
		Err(w, http.StatusConflict, "task label changed concurrently; refresh and retry")
	case errors.Is(err, workflow.ErrTaskNotFound):
		Err(w, http.StatusNotFound, "task not found")
	default:
		Err(w, http.StatusInternalServerError, err.Error())
	}
}
