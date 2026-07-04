package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// ReviewCommentsHandler manages persistent inline diff review comments on a
// task. Open comments are injected into every subsequent agent run's prompt;
// agents resolve them via the MCP sidecar's resolve_comment tool, and humans
// can resolve/reopen them from the diff viewer.
type ReviewCommentsHandler struct {
	q *gen.Queries
}

func NewReviewCommentsHandler(q *gen.Queries) *ReviewCommentsHandler {
	return &ReviewCommentsHandler{q: q}
}

// List returns all review comments for a task (open and resolved).
// Route: GET /tasks/{id}/review-comments
func (h *ReviewCommentsHandler) List(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if _, err := h.q.GetTask(r.Context(), taskID); err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	comments, err := h.q.ListTaskReviewComments(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if comments == nil {
		comments = []gen.TaskReviewComment{}
	}
	JSON(w, http.StatusOK, comments)
}

// Create adds a new open review comment to a task.
// Route: POST /tasks/{id}/review-comments
func (h *ReviewCommentsHandler) Create(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if _, err := h.q.GetTask(r.Context(), taskID); err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}

	var body struct {
		FilePath   string `json:"file_path"`
		Side       string `json:"side"`
		StartLine  int64  `json:"start_line"`
		EndLine    int64  `json:"end_line"`
		QuotedText string `json:"quoted_text"`
		Body       string `json:"body"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	body.FilePath = strings.TrimSpace(body.FilePath)
	body.Body = strings.TrimSpace(body.Body)
	if body.FilePath == "" {
		Err(w, http.StatusBadRequest, "file_path is required")
		return
	}
	if body.Body == "" {
		Err(w, http.StatusBadRequest, "body is required")
		return
	}
	if body.Side == "" {
		body.Side = "new"
	}
	if body.Side != "old" && body.Side != "new" {
		Err(w, http.StatusBadRequest, "side must be 'old' or 'new'")
		return
	}
	if body.StartLine < 1 || body.EndLine < body.StartLine {
		Err(w, http.StatusBadRequest, "invalid line range")
		return
	}

	comment, err := h.q.CreateTaskReviewComment(r.Context(), gen.CreateTaskReviewCommentParams{
		ID:         uuid.NewString(),
		TaskID:     taskID,
		FilePath:   body.FilePath,
		Side:       body.Side,
		StartLine:  body.StartLine,
		EndLine:    body.EndLine,
		QuotedText: body.QuotedText,
		Body:       body.Body,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, comment)
}

// Update resolves or reopens a review comment.
// Route: PATCH /tasks/{id}/review-comments/{comment_id}
// Body: {"status": "resolved"|"open", "resolution_note": "..."}
func (h *ReviewCommentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	commentID := chi.URLParam(r, "comment_id")

	var body struct {
		Status         string  `json:"status"`
		ResolutionNote *string `json:"resolution_note"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	switch body.Status {
	case "resolved":
		// Human-resolved: no run ID attached.
		comment, err := h.q.ResolveTaskReviewComment(r.Context(), gen.ResolveTaskReviewCommentParams{
			ResolutionNote: body.ResolutionNote,
			ID:             commentID,
			TaskID:         taskID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				Err(w, http.StatusNotFound, "comment not found or already resolved")
				return
			}
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		JSON(w, http.StatusOK, comment)

	case "open":
		comment, err := h.q.ReopenTaskReviewComment(r.Context(), gen.ReopenTaskReviewCommentParams{
			ID:     commentID,
			TaskID: taskID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				Err(w, http.StatusNotFound, "comment not found")
				return
			}
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		JSON(w, http.StatusOK, comment)

	default:
		Err(w, http.StatusBadRequest, "status must be 'resolved' or 'open'")
	}
}

// Delete removes a review comment entirely.
// Route: DELETE /tasks/{id}/review-comments/{comment_id}
func (h *ReviewCommentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	n, err := h.q.DeleteTaskReviewComment(r.Context(), gen.DeleteTaskReviewCommentParams{
		ID:     chi.URLParam(r, "comment_id"),
		TaskID: chi.URLParam(r, "id"),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		Err(w, http.StatusNotFound, "comment not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
