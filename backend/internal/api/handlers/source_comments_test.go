package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestListSourceComments_ReturnsThread verifies GET /tasks/{id}/source-comments
// returns the ingested comment thread for a task, oldest first (matching
// ListTaskSourceComments' ORDER BY external_created_at, id).
func TestListSourceComments_ReturnsThread(t *testing.T) {
	router, q, wfID, repoID := setupTaskRouter(t)

	taskID := uuid.NewString()
	if _, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: taskID, Title: "imported from github", WorkflowID: wfID, RepoID: repoID, Label: "not_ready",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Seed two comments directly via the query layer — ingestion itself is
	// tasksource's job; this handler only reads what's already there.
	if _, err := q.CreateTaskSourceComment(context.Background(), gen.CreateTaskSourceCommentParams{
		ID: uuid.NewString(), TaskID: taskID, ExternalID: "1001", Author: "alice",
		Body: "please also handle the edge case", ExternalCreatedAt: "2026-07-20T12:00:00Z",
	}); err != nil {
		t.Fatalf("seed comment 1: %v", err)
	}
	if _, err := q.CreateTaskSourceComment(context.Background(), gen.CreateTaskSourceCommentParams{
		ID: uuid.NewString(), TaskID: taskID, ExternalID: "1002", Author: "bob",
		Body: "agreed", ExternalCreatedAt: "2026-07-21T09:00:00Z",
	}); err != nil {
		t.Fatalf("seed comment 2: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/source-comments", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var comments []gen.TaskSourceComment
	if err := json.Unmarshal(w.Body.Bytes(), &comments); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].Author != "alice" || comments[1].Author != "bob" {
		t.Errorf("expected oldest-first order [alice, bob], got [%s, %s]", comments[0].Author, comments[1].Author)
	}
	if comments[0].Body != "please also handle the edge case" {
		t.Errorf("unexpected comment body: %q", comments[0].Body)
	}
}

// TestListSourceComments_EmptyForTaskWithNoComments verifies the endpoint
// returns an empty array (not null, not an error) when no comments have been
// ingested yet.
func TestListSourceComments_EmptyForTaskWithNoComments(t *testing.T) {
	router, q, wfID, repoID := setupTaskRouter(t)

	taskID := uuid.NewString()
	if _, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: taskID, Title: "no comments yet", WorkflowID: wfID, RepoID: repoID, Label: "not_ready",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/source-comments", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("expected empty array body, got %q", got)
	}
}

// TestListSourceComments_UnknownTask404s verifies the endpoint 404s for a
// task id that doesn't exist, matching the other task sub-resource handlers.
func TestListSourceComments_UnknownTask404s(t *testing.T) {
	router, _, _, _ := setupTaskRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+uuid.NewString()+"/source-comments", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown task, got %d: %s", w.Code, w.Body.String())
	}
}
