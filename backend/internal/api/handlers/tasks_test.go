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

// apiTask mirrors the JSON wire format returned by the tasks handler.
// The Attachments field is []string because the handler serialises the stored
// JSON string as a proper JSON array, not a raw string.
type apiTask struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	RepoID      string   `json:"repo_id"`
	WorkflowID  string   `json:"workflow_id"`
	AgentNotes  string   `json:"agent_notes"`
	Attachments []string `json:"attachments"`
	Paused      bool     `json:"paused"`
}

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

	h := handlers.NewTasksHandler(q, engine, t.TempDir())

	r := chi.NewRouter()
	r.Get("/tasks", h.List)
	r.Post("/tasks", h.Create)
	r.Get("/tasks/{id}", h.Get)
	r.Patch("/tasks/{id}", h.Update)
	r.Delete("/tasks/{id}", h.Delete)
	r.Patch("/tasks/{id}/label", h.MoveLabel)
	r.Get("/tasks/{id}/runs", h.ListRuns)
	r.Patch("/tasks/{id}/pause", h.SetPaused)

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
	var task apiTask
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
	if task.Attachments == nil {
		t.Errorf("attachments: expected empty array, got nil")
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
	var tasks []apiTask
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
		Label:      "work",
	})
	_, _ = q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Task B",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "done",
	})

	req := httptest.NewRequest(http.MethodGet, "/tasks?label=work", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var tasks []apiTask
	_ = json.NewDecoder(w.Body).Decode(&tasks)
	for _, task := range tasks {
		if task.Label != "work" {
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
		Label:      "work",
	})

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got apiTask
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
		Label:      "work",
	})

	body := map[string]string{"title": "Updated Title", "description": "new desc", "type": "bug"}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var updated apiTask
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
		Label:      "work",
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
	var updated apiTask
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
		Label:      "work",
	})

	// work → done has no direct transition defined
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
		Label:      "work",
	})

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/runs", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ---------- Pause ----------

func TestTasks_SetPaused_OK(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Pausable",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "work",
	})

	body := map[string]bool{"paused": true}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID+"/pause", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var updated apiTask
	_ = json.NewDecoder(w.Body).Decode(&updated)
	if !updated.Paused {
		t.Errorf("expected paused=true in response")
	}
	if updated.Label != "work" {
		t.Errorf("expected label to remain unchanged, got %q", updated.Label)
	}

	// Confirm persisted via a separate GET.
	getReq := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID, nil)
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, getReq)
	var fetched apiTask
	_ = json.NewDecoder(getW.Body).Decode(&fetched)
	if !fetched.Paused {
		t.Errorf("expected paused to persist, got %+v", fetched)
	}
}

func TestTasks_SetPaused_Unpause(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Resumable",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "work",
	})

	if _, err := q.SetTaskPaused(context.Background(), gen.SetTaskPausedParams{Paused: 1, ID: task.ID}); err != nil {
		t.Fatalf("seed paused: %v", err)
	}

	body := map[string]bool{"paused": false}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID+"/pause", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var updated apiTask
	_ = json.NewDecoder(w.Body).Decode(&updated)
	if updated.Paused {
		t.Errorf("expected paused=false in response")
	}
}

// TestTasks_Paused_ExcludedFromAgentPickup confirms the dispatcher's
// ListAgentPickupTasks query never returns a paused task, even when its
// label is otherwise eligible for agent pickup.
func TestTasks_Paused_ExcludedFromAgentPickup(t *testing.T) {
	_, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()

	// "work" is an agent-trigger label in the seeded default workflow.
	task, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Eligible for pickup",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "plan",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{Label: "work", ID: task.ID}); err != nil {
		t.Fatalf("move label: %v", err)
	}

	pickup, err := q.ListAgentPickupTasks(ctx)
	if err != nil {
		t.Fatalf("list pickup: %v", err)
	}
	found := false
	for _, p := range pickup {
		if p.ID == task.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unpaused eligible task to be returned by ListAgentPickupTasks")
	}

	if _, err := q.SetTaskPaused(ctx, gen.SetTaskPausedParams{Paused: 1, ID: task.ID}); err != nil {
		t.Fatalf("pause task: %v", err)
	}

	pickup, err = q.ListAgentPickupTasks(ctx)
	if err != nil {
		t.Fatalf("list pickup after pause: %v", err)
	}
	for _, p := range pickup {
		if p.ID == task.ID {
			t.Fatalf("expected paused task to be excluded from ListAgentPickupTasks")
		}
	}
}
