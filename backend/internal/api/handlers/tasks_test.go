package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// noopPub satisfies agent.Publisher / workflow.Publisher without doing anything.
type noopPub struct{}

func (noopPub) Publish(string, map[string]any) {}

// openTestDB creates a temp SQLite database, seeds the default workflow,
// and registers cleanup functions.
func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp("", "handler-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := storage.SeedDefaultWorkflow(context.Background(), db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// setupTaskRouter wires a chi router with tasks routes and returns the
// underlying queries, workflow ID, and repo ID for use in tests.
func setupTaskRouter(t *testing.T) (http.Handler, *gen.Queries, string, string) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), noopPub{})

	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID

	repoID := uuid.NewString()
	_, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:         repoID,
		Name:       "test-repo",
		Path:       t.TempDir(),
		WorkflowID: &wfID,
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	h := handlers.NewTasksHandler(q, engine)

	r := chi.NewRouter()
	r.Get("/tasks", h.List)
	r.Post("/tasks", h.Create)
	r.Get("/tasks/{id}", h.Get)
	r.Patch("/tasks/{id}", h.Update)
	r.Delete("/tasks/{id}", h.Delete)
	r.Patch("/tasks/{id}/label", h.MoveLabel)
	r.Get("/tasks/{id}/runs", h.ListRuns)

	return r, q, wfID, repoID
}

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

// ---------- Create ----------

func TestTasks_Create_OK(t *testing.T) {
	r, _, wfID, repoID := setupTaskRouter(t)

	body := map[string]string{
		"title":       "Fix the bug",
		"description": "details here",
		"repo_id":     repoID,
		"workflow_id": wfID,
	}
	req := httptest.NewRequest(http.MethodPost, "/tasks", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body)
	}
	var task gen.Task
	if err := json.NewDecoder(w.Body).Decode(&task); err != nil {
		t.Fatal(err)
	}
	if task.Title != "Fix the bug" {
		t.Errorf("title: want %q, got %q", "Fix the bug", task.Title)
	}
	if task.Label != "not_ready" {
		t.Errorf("initial label: want 'not_ready', got %q", task.Label)
	}
	if task.Type != "feature" {
		t.Errorf("default type: want 'feature', got %q", task.Type)
	}
}

func TestTasks_Create_MissingTitle_Returns400(t *testing.T) {
	r, _, wfID, repoID := setupTaskRouter(t)

	body := map[string]string{"repo_id": repoID, "workflow_id": wfID}
	req := httptest.NewRequest(http.MethodPost, "/tasks", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestTasks_Create_MissingRepoAndWorkflow_Returns400(t *testing.T) {
	r, _, _, _ := setupTaskRouter(t)

	body := map[string]string{"title": "only title"}
	req := httptest.NewRequest(http.MethodPost, "/tasks", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ---------- List ----------

func TestTasks_List_Empty(t *testing.T) {
	r, _, _, _ := setupTaskRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var tasks []gen.Task
	if err := json.NewDecoder(w.Body).Decode(&tasks); err != nil {
		t.Fatal(err)
	}
	// tasks may be null in JSON; both nil and empty slice are acceptable
}

func TestTasks_List_WithLabel(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	_, _ = q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Task A",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "todo",
	})
	_, _ = q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Task B",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "done",
	})

	req := httptest.NewRequest(http.MethodGet, "/tasks?label=todo", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var tasks []gen.Task
	_ = json.NewDecoder(w.Body).Decode(&tasks)
	for _, task := range tasks {
		if task.Label != "todo" {
			t.Errorf("label filter returned task with label %q", task.Label)
		}
	}
}

// ---------- Get ----------

func TestTasks_Get_NotFound(t *testing.T) {
	r, _, _, _ := setupTaskRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/tasks/does-not-exist", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestTasks_Get_Found(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Fetched",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "todo",
	})

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got gen.Task
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.ID != task.ID {
		t.Errorf("expected task ID %s, got %s", task.ID, got.ID)
	}
}

// ---------- Update ----------

func TestTasks_Update_OK(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Original",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "todo",
	})

	body := map[string]string{"title": "Updated Title", "description": "new desc", "type": "bug"}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var updated gen.Task
	_ = json.NewDecoder(w.Body).Decode(&updated)
	if updated.Title != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got %q", updated.Title)
	}
	if updated.Type != "bug" {
		t.Errorf("expected type 'bug', got %q", updated.Type)
	}
}

// ---------- Delete ----------

func TestTasks_Delete_OK(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "To Delete",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "todo",
	})

	req := httptest.NewRequest(http.MethodDelete, "/tasks/"+task.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}

	// Subsequent GET should 404
	req2 := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", w2.Code)
	}
}

// ---------- MoveLabel ----------

func TestTasks_MoveLabel_ValidTransition(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	// not_ready → plan is a valid human transition in the default workflow
	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Label Mover",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "not_ready",
	})

	body := map[string]string{"to_label": "plan"}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID+"/label", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var updated gen.Task
	_ = json.NewDecoder(w.Body).Decode(&updated)
	if updated.Label != "plan" {
		t.Errorf("expected label 'plan', got %q", updated.Label)
	}
}

func TestTasks_MoveLabel_MissingToLabel_Returns400(t *testing.T) {
	r, _, _, _ := setupTaskRouter(t)

	body := map[string]string{}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/any-id/label", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestTasks_MoveLabel_InvalidTransition_Returns400(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "No jump",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "todo",
	})

	// todo → done has no direct transition defined
	body := map[string]string{"to_label": "done"}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID+"/label", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid transition, got %d", w.Code)
	}
}

// ---------- Runs ----------

func TestTasks_ListRuns_Empty(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "No runs yet",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "todo",
	})

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/runs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
