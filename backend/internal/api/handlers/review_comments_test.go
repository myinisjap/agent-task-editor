package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// setupReviewCommentRouter wires the review-comments routes plus a task to
// comment on, returning the router and the task ID.
func setupReviewCommentRouter(t *testing.T) (http.Handler, *gen.Queries, string) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())

	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID: repoID, Name: "test-repo", Path: t.TempDir(), WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	taskID := uuid.NewString()
	if _, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: taskID, Title: "review me", WorkflowID: wfID, RepoID: repoID, Label: "review",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	h := handlers.NewReviewCommentsHandler(q)
	r := chi.NewRouter()
	r.Get("/tasks/{id}/review-comments", h.List)
	r.Post("/tasks/{id}/review-comments", h.Create)
	r.Patch("/tasks/{id}/review-comments/{comment_id}", h.Update)
	r.Delete("/tasks/{id}/review-comments/{comment_id}", h.Delete)
	return r, q, taskID
}

func createComment(t *testing.T, r http.Handler, taskID string, body map[string]any) gen.TaskReviewComment {
	t.Helper()
	req := httptest.NewRequest("POST", "/tasks/"+taskID+"/review-comments", jsonBody(t, body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create comment: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var c gen.TaskReviewComment
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode comment: %v", err)
	}
	return c
}

func TestReviewComments_CreateAndList(t *testing.T) {
	r, _, taskID := setupReviewCommentRouter(t)

	c := createComment(t, r, taskID, map[string]any{
		"file_path": "main.go", "side": "new", "start_line": 10, "end_line": 12,
		"quoted_text": "x := 1", "body": "use the helper",
	})
	if c.Status != "open" {
		t.Errorf("expected new comment open, got %q", c.Status)
	}
	if c.TaskID != taskID {
		t.Errorf("expected task_id %q, got %q", taskID, c.TaskID)
	}

	req := httptest.NewRequest("GET", "/tasks/"+taskID+"/review-comments", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var list []gen.TaskReviewComment
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != c.ID {
		t.Errorf("expected 1 comment (%s), got %+v", c.ID, list)
	}
}

func TestReviewComments_Create_Validation(t *testing.T) {
	r, _, taskID := setupReviewCommentRouter(t)

	cases := []map[string]any{
		{"side": "new", "start_line": 1, "end_line": 1, "body": "x"},                          // missing file_path
		{"file_path": "a.go", "side": "new", "start_line": 1, "end_line": 1},                  // missing body
		{"file_path": "a.go", "side": "sideways", "start_line": 1, "end_line": 1, "body": "x"}, // bad side
		{"file_path": "a.go", "side": "new", "start_line": 0, "end_line": 1, "body": "x"},     // bad start_line
		{"file_path": "a.go", "side": "new", "start_line": 5, "end_line": 2, "body": "x"},     // end < start
	}
	for i, body := range cases {
		req := httptest.NewRequest("POST", "/tasks/"+taskID+"/review-comments", jsonBody(t, body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("case %d: expected 400, got %d: %s", i, w.Code, w.Body.String())
		}
	}

	// Unknown task → 404.
	req := httptest.NewRequest("POST", "/tasks/"+uuid.NewString()+"/review-comments",
		jsonBody(t, map[string]any{"file_path": "a.go", "start_line": 1, "end_line": 1, "body": "x"}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown task: expected 404, got %d", w.Code)
	}
}

func TestReviewComments_ResolveAndReopen(t *testing.T) {
	r, _, taskID := setupReviewCommentRouter(t)
	c := createComment(t, r, taskID, map[string]any{
		"file_path": "main.go", "start_line": 3, "end_line": 3, "body": "rename this",
	})

	// Resolve.
	req := httptest.NewRequest("PATCH", "/tasks/"+taskID+"/review-comments/"+c.ID,
		jsonBody(t, map[string]any{"status": "resolved", "resolution_note": "renamed"}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("resolve: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resolved gen.TaskReviewComment
	_ = json.Unmarshal(w.Body.Bytes(), &resolved)
	if resolved.Status != "resolved" || resolved.ResolutionNote == nil || *resolved.ResolutionNote != "renamed" {
		t.Errorf("unexpected resolved comment: %+v", resolved)
	}

	// Resolving again → 404 (already resolved).
	req = httptest.NewRequest("PATCH", "/tasks/"+taskID+"/review-comments/"+c.ID,
		jsonBody(t, map[string]any{"status": "resolved"}))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("double resolve: expected 404, got %d", w.Code)
	}

	// Reopen clears resolution fields.
	req = httptest.NewRequest("PATCH", "/tasks/"+taskID+"/review-comments/"+c.ID,
		jsonBody(t, map[string]any{"status": "open"}))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reopen: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var reopened gen.TaskReviewComment
	_ = json.Unmarshal(w.Body.Bytes(), &reopened)
	if reopened.Status != "open" || reopened.ResolutionNote != nil || reopened.ResolvedByRunID != nil {
		t.Errorf("unexpected reopened comment: %+v", reopened)
	}
}

func TestReviewComments_Delete(t *testing.T) {
	r, _, taskID := setupReviewCommentRouter(t)
	c := createComment(t, r, taskID, map[string]any{
		"file_path": "main.go", "start_line": 1, "end_line": 1, "body": "delete me",
	})

	req := httptest.NewRequest("DELETE", "/tasks/"+taskID+"/review-comments/"+c.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", w.Code)
	}

	// Deleting again → 404.
	req = httptest.NewRequest("DELETE", "/tasks/"+taskID+"/review-comments/"+c.ID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("double delete: expected 404, got %d", w.Code)
	}
}
