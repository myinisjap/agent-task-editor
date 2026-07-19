package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/writeback"
)

// apiTask mirrors the JSON wire format returned by the tasks handler.
// The Attachments field is []string because the handler serialises the stored
// JSON string as a proper JSON array, not a raw string.
type apiTask struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Type          string   `json:"type"`
	Label         string   `json:"label"`
	RepoID        string   `json:"repo_id"`
	WorkflowID    string   `json:"workflow_id"`
	AgentNotes    string   `json:"agent_notes"`
	Attachments   []string `json:"attachments"`
	Paused        bool     `json:"paused"`
	Archived      bool     `json:"archived"`
	Priority      int      `json:"priority"`
	QueuePosition *int     `json:"queue_position"`
}

// noopPub satisfies agent.Publisher / workflow.Publisher without doing anything.
type noopPub struct{}

func (noopPub) Publish(string, map[string]any) {}

// fakeCanceller records the run IDs it was asked to cancel and reports whether
// each was "active" via the found map. saturated controls what Saturated()
// reports (defaults to false, i.e. pool has idle capacity).
type fakeCanceller struct {
	called    []string
	found     map[string]bool
	saturated bool
}

func (c *fakeCanceller) Cancel(runID string) bool {
	c.called = append(c.called, runID)
	return c.found[runID]
}

func (c *fakeCanceller) Saturated() bool {
	return c.saturated
}

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

	h := handlers.NewTasksHandler(q, engine, t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil)

	r := chi.NewRouter()
	r.Get("/tasks", h.List)
	r.Post("/tasks", h.Create)
	r.Post("/tasks/bulk", h.Bulk)
	r.Get("/tasks/{id}", h.Get)
	r.Patch("/tasks/{id}", h.Update)
	r.Delete("/tasks/{id}", h.Delete)
	r.Patch("/tasks/{id}/label", h.MoveLabel)
	r.Get("/tasks/{id}/runs", h.ListRuns)
	r.Get("/tasks/{id}/runs/{run_id}/logs", h.GetRunLogs)
	r.Post("/tasks/{id}/runs/{run_id}/cancel", h.CancelRun)
	r.Patch("/tasks/{id}/pause", h.SetPaused)
	r.Patch("/tasks/{id}/archive", h.SetArchived)
	r.Post("/tasks/{id}/pr", h.CreatePR)
	r.Patch("/tasks/{id}/git-state", h.UpdateGitState)

	return r, q, wfID, repoID
}

// setupTaskRouterWithWriteback is like setupTaskRouter but also wires a
// writeback.Writeback backed by fake gh-calling functions onto the handler,
// returning the fake so tests can assert on write-back actions without
// shelling out to a real gh binary.
func setupTaskRouterWithWriteback(t *testing.T) (http.Handler, *gen.Queries, string, string, *fakeWriteback) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), noopPub{})

	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:         repoID,
		Name:       "test-repo",
		Path:       t.TempDir(),
		WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	h := handlers.NewTasksHandler(q, engine, t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil)

	fwb := &fakeWriteback{}
	h.SetWriteback(writeback.NewWithClient(q,
		func(ctx context.Context, repoName string, issueNumber int, label string) error {
			fwb.labelCalls = append(fwb.labelCalls, label)
			return nil
		},
		func(ctx context.Context, repoName string, issueNumber int, body string) error {
			fwb.commentCalls = append(fwb.commentCalls, body)
			return nil
		},
		func(ctx context.Context, repoName string, issueNumber int, body string) error {
			fwb.closeCalls = append(fwb.closeCalls, body)
			return nil
		},
	))

	r := chi.NewRouter()
	r.Patch("/tasks/{id}/git-state", h.UpdateGitState)
	r.Post("/tasks/{id}/pr", h.CreatePR)
	r.Get("/tasks/{id}/github-status", h.GitHubStatus)

	return r, q, wfID, repoID, fwb
}

// fakeWriteback records calls made through the writeback seam, mirroring the
// same helper used by internal/ghsync's tests.
type fakeWriteback struct {
	labelCalls   []string
	commentCalls []string
	closeCalls   []string
}

// setupCancelRouter wires just the cancel route against a caller-supplied
// canceller so tests can assert on what the handler signals.
func setupCancelRouter(t *testing.T, canceller handlers.RunCanceller) (http.Handler, *gen.Queries, string, string) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), noopPub{})

	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:         repoID,
		Name:       "test-repo",
		Path:       t.TempDir(),
		WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	h := handlers.NewTasksHandler(q, engine, t.TempDir(), canceller, nil)
	r := chi.NewRouter()
	r.Post("/tasks/{id}/runs/{run_id}/cancel", h.CancelRun)
	return r, q, wfID, repoID
}

// seedRunningRun creates a task plus an agent run in the given status and
// returns their IDs.
func seedRunningRun(t *testing.T, q *gen.Queries, wfID, repoID, status string) (taskID, runID string) {
	t.Helper()
	ctx := context.Background()
	taskID = uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: taskID, Title: "Cancel me", WorkflowID: wfID, RepoID: repoID, Label: "work",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	runID = uuid.NewString()
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: taskID}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := q.UpdateAgentRunStatus(ctx, gen.UpdateAgentRunStatusParams{Status: status, ID: runID}); err != nil {
		t.Fatalf("set run status: %v", err)
	}
	return taskID, runID
}

func TestTasks_CancelRun_OK(t *testing.T) {
	c := &fakeCanceller{found: map[string]bool{}}
	r, q, wfID, repoID := setupCancelRouter(t, c)
	taskID, runID := seedRunningRun(t, q, wfID, repoID, "running")
	c.found[runID] = true

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/runs/"+runID+"/cancel", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", w.Code, w.Body.String())
	}
	if len(c.called) != 1 || c.called[0] != runID {
		t.Errorf("expected Cancel(%s) once, got %v", runID, c.called)
	}
}

func TestTasks_CancelRun_NotRunning(t *testing.T) {
	c := &fakeCanceller{found: map[string]bool{}}
	r, q, wfID, repoID := setupCancelRouter(t, c)
	taskID, runID := seedRunningRun(t, q, wfID, repoID, "completed")

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/runs/"+runID+"/cancel", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
	if len(c.called) != 0 {
		t.Errorf("expected Cancel not called for a non-running run, got %v", c.called)
	}
}

func TestTasks_CancelRun_NoLongerActive(t *testing.T) {
	// Run is 'running' in the DB but the pool no longer has it registered.
	c := &fakeCanceller{found: map[string]bool{}}
	r, q, wfID, repoID := setupCancelRouter(t, c)
	taskID, runID := seedRunningRun(t, q, wfID, repoID, "running")

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/runs/"+runID+"/cancel", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestTasks_CancelRun_WrongTask(t *testing.T) {
	c := &fakeCanceller{found: map[string]bool{}}
	r, q, wfID, repoID := setupCancelRouter(t, c)
	_, runID := seedRunningRun(t, q, wfID, repoID, "running")

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+uuid.NewString()+"/runs/"+runID+"/cancel", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
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

func TestTasks_Create_WithLabel_LandsDirectlyOnColumn(t *testing.T) {
	r, _, wfID, repoID := setupTaskRouter(t)

	// "work" is not reachable from "not_ready" via a transition edge, but initial
	// placement is not a transition — the task should land straight on "work".
	body := map[string]string{
		"title":       "Ship it",
		"repo_id":     repoID,
		"workflow_id": wfID,
		"label":       "work",
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
	if task.Label != "work" {
		t.Errorf("initial label: want 'work', got %q", task.Label)
	}
}

func TestTasks_Create_UnknownLabel_Returns400(t *testing.T) {
	r, _, wfID, repoID := setupTaskRouter(t)

	body := map[string]string{
		"title":       "bad label",
		"repo_id":     repoID,
		"workflow_id": wfID,
		"label":       "nonexistent",
	}
	req := httptest.NewRequest(http.MethodPost, "/tasks", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown label, got %d: %s", w.Code, w.Body)
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

func TestTasks_Create_WithPriority_OK(t *testing.T) {
	r, _, wfID, repoID := setupTaskRouter(t)

	body := map[string]any{
		"title":       "Urgent fix",
		"repo_id":     repoID,
		"workflow_id": wfID,
		"priority":    2,
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
	if task.Priority != 2 {
		t.Errorf("expected priority 2, got %d", task.Priority)
	}
}

func TestTasks_Create_DefaultPriority_IsNormal(t *testing.T) {
	r, _, wfID, repoID := setupTaskRouter(t)

	body := map[string]string{"title": "No priority given", "repo_id": repoID, "workflow_id": wfID}
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
	if task.Priority != 0 {
		t.Errorf("expected default priority 0 (normal), got %d", task.Priority)
	}
}

func TestTasks_Create_InvalidPriority_Returns400(t *testing.T) {
	r, _, wfID, repoID := setupTaskRouter(t)

	body := map[string]any{
		"title":       "Bad priority",
		"repo_id":     repoID,
		"workflow_id": wfID,
		"priority":    99,
	}
	req := httptest.NewRequest(http.MethodPost, "/tasks", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body)
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

func TestTasks_Update_Priority_OK(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Original",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "work",
	})

	body := map[string]any{"priority": 1}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var updated apiTask
	_ = json.NewDecoder(w.Body).Decode(&updated)
	if updated.Priority != 1 {
		t.Errorf("expected priority 1, got %d", updated.Priority)
	}
}

func TestTasks_Update_PriorityOmitted_Preserved(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Original",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "work",
		Priority:   2,
	})

	body := map[string]string{"title": "Renamed only"}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var updated apiTask
	_ = json.NewDecoder(w.Body).Decode(&updated)
	if updated.Priority != 2 {
		t.Errorf("expected priority to be preserved at 2, got %d", updated.Priority)
	}
}

func TestTasks_Update_InvalidPriority_Returns400(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Original",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "work",
	})

	body := map[string]any{"priority": -7}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body)
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

// ---------- List filters / search ----------

// listTasks is a helper that GETs /tasks with the given query string and
// decodes the response.
func listTasks(t *testing.T, r http.Handler, query string) []apiTask {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/tasks"+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /tasks%s: expected 200, got %d: %s", query, w.Code, w.Body)
	}
	var tasks []apiTask
	if err := json.NewDecoder(w.Body).Decode(&tasks); err != nil {
		t.Fatal(err)
	}
	return tasks
}

func TestTasks_List_SearchAndFilters(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()

	mk := func(title, desc, typ string) apiTask {
		task, err := q.CreateTask(ctx, gen.CreateTaskParams{
			ID:          uuid.NewString(),
			Title:       title,
			Description: desc,
			Type:        typ,
			WorkflowID:  wfID,
			RepoID:      repoID,
			Label:       "work",
		})
		if err != nil {
			t.Fatalf("create task: %v", err)
		}
		return apiTask{ID: task.ID}
	}
	flaky := mk("Fix flaky test", "the websocket test flakes", "bug")
	mk("Upgrade dependency", "bump react to 19", "chore")
	upgradeDesc := mk("Ship dark mode", "also upgrade tailwind while in there", "feature")

	// Free-text search matches title...
	got := listTasks(t, r, "?q=flaky")
	if len(got) != 1 || got[0].ID != flaky.ID {
		t.Errorf("q=flaky: expected exactly the flaky task, got %+v", got)
	}
	// ...and description, case-insensitively.
	got = listTasks(t, r, "?q=UPGRADE")
	if len(got) != 2 {
		t.Errorf("q=UPGRADE: expected 2 tasks (title + description match), got %d", len(got))
	}
	// Search combined with a type filter.
	got = listTasks(t, r, "?q=upgrade&type=feature")
	if len(got) != 1 || got[0].ID != upgradeDesc.ID {
		t.Errorf("q=upgrade&type=feature: expected the dark mode task, got %+v", got)
	}
	// repo_id filter: matching repo returns everything, unknown repo nothing.
	if got = listTasks(t, r, "?repo_id="+repoID); len(got) != 3 {
		t.Errorf("repo_id filter: expected 3 tasks, got %d", len(got))
	}
	if got = listTasks(t, r, "?repo_id=nope"); len(got) != 0 {
		t.Errorf("repo_id=nope: expected 0 tasks, got %d", len(got))
	}
	// git_state filter: nothing has a git state yet.
	if got = listTasks(t, r, "?git_state=pr_open"); len(got) != 0 {
		t.Errorf("git_state=pr_open: expected 0 tasks, got %d", len(got))
	}
}

func TestTasks_List_ArchivedFilter(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()

	live, _ := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Live", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	archived, _ := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Old", WorkflowID: wfID, RepoID: repoID, Label: "done",
	})
	if _, err := q.SetTaskArchived(ctx, gen.SetTaskArchivedParams{Archived: 1, ID: archived.ID}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Default view hides archived tasks.
	got := listTasks(t, r, "")
	if len(got) != 1 || got[0].ID != live.ID {
		t.Errorf("default list: expected only the live task, got %+v", got)
	}
	// archived=only returns just archived tasks.
	got = listTasks(t, r, "?archived=only")
	if len(got) != 1 || got[0].ID != archived.ID || !got[0].Archived {
		t.Errorf("archived=only: expected only the archived task, got %+v", got)
	}
	// archived=all returns everything.
	if got = listTasks(t, r, "?archived=all"); len(got) != 2 {
		t.Errorf("archived=all: expected 2 tasks, got %d", len(got))
	}
	// Invalid value is rejected.
	req := httptest.NewRequest(http.MethodGet, "/tasks?archived=maybe", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("archived=maybe: expected 400, got %d", w.Code)
	}
}

// ---------- Archive ----------

func TestTasks_SetArchived_OK(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Archivable", WorkflowID: wfID, RepoID: repoID, Label: "done",
	})

	body := map[string]bool{"archived": true}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID+"/archive", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var updated apiTask
	_ = json.NewDecoder(w.Body).Decode(&updated)
	if !updated.Archived {
		t.Errorf("expected archived=true in response")
	}
	if updated.Label != "done" {
		t.Errorf("expected label to remain unchanged, got %q", updated.Label)
	}
}

func TestTasks_SetArchived_NotFound(t *testing.T) {
	r, _, _, _ := setupTaskRouter(t)

	body := map[string]bool{"archived": true}
	req := httptest.NewRequest(http.MethodPatch, "/tasks/does-not-exist/archive", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestTasks_Archived_ExcludedFromAgentPickup confirms the dispatcher's
// ListAgentPickupTasks query never returns an archived task, even when its
// label is otherwise eligible for agent pickup.
func TestTasks_Archived_ExcludedFromAgentPickup(t *testing.T) {
	_, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()

	task, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Eligible", WorkflowID: wfID, RepoID: repoID, Label: "plan",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{Label: "work", ID: task.ID}); err != nil {
		t.Fatalf("move label: %v", err)
	}
	if _, err := q.SetTaskArchived(ctx, gen.SetTaskArchivedParams{Archived: 1, ID: task.ID}); err != nil {
		t.Fatalf("archive task: %v", err)
	}

	pickup, err := q.ListAgentPickupTasks(ctx)
	if err != nil {
		t.Fatalf("list pickup: %v", err)
	}
	for _, p := range pickup {
		if p.ID == task.ID {
			t.Fatalf("expected archived task to be excluded from ListAgentPickupTasks")
		}
	}
}

// ---------- Bulk ----------

type bulkResponse struct {
	Results []struct {
		ID    string `json:"id"`
		Ok    bool   `json:"ok"`
		Error string `json:"error"`
	} `json:"results"`
}

func TestTasks_Bulk_Archive(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()

	a, _ := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "A", WorkflowID: wfID, RepoID: repoID, Label: "done",
	})
	b, _ := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "B", WorkflowID: wfID, RepoID: repoID, Label: "done",
	})

	body := map[string]any{"ids": []string{a.ID, b.ID}, "action": "archive"}
	req := httptest.NewRequest(http.MethodPost, "/tasks/bulk", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var resp bulkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Results) != 2 || !resp.Results[0].Ok || !resp.Results[1].Ok {
		t.Fatalf("expected both results ok, got %+v", resp.Results)
	}

	for _, id := range []string{a.ID, b.ID} {
		task, err := q.GetTask(ctx, id)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task.Archived == 0 {
			t.Errorf("task %s: expected archived", id)
		}
	}
}

func TestTasks_Bulk_Move_PartialFailure(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()

	// "not_ready" → "plan" is a valid human transition in the seeded workflow.
	movable, _ := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Movable", WorkflowID: wfID, RepoID: repoID, Label: "not_ready",
	})

	body := map[string]any{"ids": []string{movable.ID, "does-not-exist"}, "action": "move", "to_label": "plan"}
	req := httptest.NewRequest(http.MethodPost, "/tasks/bulk", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d: %s", w.Code, w.Body)
	}
	var resp bulkResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %+v", resp.Results)
	}
	if !resp.Results[0].Ok {
		t.Errorf("expected move of existing task to succeed: %+v", resp.Results[0])
	}
	if resp.Results[1].Ok || resp.Results[1].Error == "" {
		t.Errorf("expected move of missing task to fail with an error: %+v", resp.Results[1])
	}

	moved, _ := q.GetTask(ctx, movable.ID)
	if moved.Label != "plan" {
		t.Errorf("expected label 'plan' after bulk move, got %q", moved.Label)
	}
}

func TestTasks_Bulk_Validation(t *testing.T) {
	r, _, _, _ := setupTaskRouter(t)

	cases := []map[string]any{
		{"ids": []string{}, "action": "archive"},    // empty ids
		{"ids": []string{"x"}, "action": "explode"}, // unknown action
		{"ids": []string{"x"}, "action": "move"},    // move without to_label
	}
	for i, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/tasks/bulk", jsonBody(t, body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("case %d: expected 400, got %d", i, w.Code)
		}
	}
}

func TestTasks_Bulk_Pause(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()

	task, _ := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "P", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})

	body := map[string]any{"ids": []string{task.ID}, "action": "pause"}
	req := httptest.NewRequest(http.MethodPost, "/tasks/bulk", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	got, _ := q.GetTask(ctx, task.ID)
	if got.Paused == 0 {
		t.Errorf("expected task paused after bulk pause")
	}

	body = map[string]any{"ids": []string{task.ID}, "action": "resume"}
	req = httptest.NewRequest(http.MethodPost, "/tasks/bulk", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	got, _ = q.GetTask(ctx, task.ID)
	if got.Paused != 0 {
		t.Errorf("expected task unpaused after bulk resume")
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

// setupQueuePositionRouter wires GET /tasks/{id} against a canceller whose
// Saturated() reports the given value, so tests can assert queue_position
// gating on pool saturation.
func setupQueuePositionRouter(t *testing.T, saturated bool) (http.Handler, *gen.Queries, string, string) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), noopPub{})

	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:         repoID,
		Name:       "test-repo",
		Path:       t.TempDir(),
		WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	h := handlers.NewTasksHandler(q, engine, t.TempDir(), &fakeCanceller{found: map[string]bool{}, saturated: saturated}, nil)
	r := chi.NewRouter()
	r.Get("/tasks/{id}", h.Get)
	return r, q, wfID, repoID
}

// TestTasks_Get_QueuePosition verifies GET /tasks/{id} surfaces a derived
// queue_position for a task currently eligible for agent pickup when the
// worker pool is saturated, and omits it (nil) for a task that is not
// eligible (paused) regardless of saturation.
func TestTasks_Get_QueuePosition(t *testing.T) {
	r, q, wfID, repoID := setupQueuePositionRouter(t, true)
	ctx := context.Background()

	// "plan" is an agent-trigger label in the seeded default workflow.
	eligible, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Eligible",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "plan",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	notEligible, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Paused, not eligible",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "plan",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := q.SetTaskPaused(ctx, gen.SetTaskPausedParams{Paused: 1, ID: notEligible.ID}); err != nil {
		t.Fatalf("pause task: %v", err)
	}

	// Eligible task should have a non-nil queue position when the pool is saturated.
	req := httptest.NewRequest(http.MethodGet, "/tasks/"+eligible.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var got apiTask
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.QueuePosition == nil {
		t.Fatalf("expected non-nil queue_position for an eligible task when pool is saturated")
	}
	if *got.QueuePosition < 0 {
		t.Errorf("expected a non-negative queue_position, got %d", *got.QueuePosition)
	}

	// Paused task should have a nil queue position even when saturated.
	req = httptest.NewRequest(http.MethodGet, "/tasks/"+notEligible.ID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var gotPaused apiTask
	_ = json.NewDecoder(w.Body).Decode(&gotPaused)
	if gotPaused.QueuePosition != nil {
		t.Errorf("expected nil queue_position for a paused (non-eligible) task, got %d", *gotPaused.QueuePosition)
	}
}

// TestTasks_Get_QueuePosition_NotSaturated verifies that an eligible task's
// queue_position stays nil when the worker pool has idle capacity — it will
// be picked up on the next sweep rather than actually waiting, so the
// "queued" badge should not be shown.
func TestTasks_Get_QueuePosition_NotSaturated(t *testing.T) {
	r, q, wfID, repoID := setupQueuePositionRouter(t, false)
	ctx := context.Background()

	eligible, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Eligible",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "plan",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+eligible.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var got apiTask
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.QueuePosition != nil {
		t.Errorf("expected nil queue_position for an eligible task when pool has idle capacity, got %d", *got.QueuePosition)
	}
}

// ---------- CreatePR (POST /tasks/{id}/pr) ----------

func TestCreatePR_TaskNotFound_Returns404(t *testing.T) {
	r, _, _, _ := setupTaskRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/tasks/does-not-exist/pr", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body)
	}
}

func TestCreatePR_NoBranch_Returns400(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Needs a branch",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "work",
	})

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/pr", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "no branch") {
		t.Errorf("expected 'no branch' error, got %s", w.Body)
	}
}

func TestCreatePR_RepoWithoutRemote_Returns400(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)

	// The test repo is created without a remote_url, so a branched task is
	// rejected before any push/gh invocation.
	task, _ := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Has a branch",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "work",
	})
	if err := q.SetTaskWorktree(context.Background(), gen.SetTaskWorktreeParams{
		Branch:  "ate-has-a-branch-1234",
		BaseRef: "origin/main",
		ID:      task.ID,
	}); err != nil {
		t.Fatalf("set worktree: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/pr", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "remote_url") {
		t.Errorf("expected 'remote_url' error, got %s", w.Body)
	}
}

// ---------- Pagination ----------

// listTasksPage GETs /tasks with the given query and returns the decoded tasks
// plus the X-Next-Cursor header.
func listTasksPage(t *testing.T, r http.Handler, query string) ([]apiTask, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/tasks"+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /tasks%s: expected 200, got %d: %s", query, w.Code, w.Body)
	}
	var tasks []apiTask
	if err := json.NewDecoder(w.Body).Decode(&tasks); err != nil {
		t.Fatal(err)
	}
	return tasks, w.Header().Get("X-Next-Cursor")
}

func TestTasks_List_Pagination(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()

	// Create 5 tasks; created_at is assigned by CURRENT_TIMESTAMP so several may
	// share a second — the (created_at, id) cursor must still not skip or repeat.
	for i := 0; i < 5; i++ {
		if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
			ID:         uuid.NewString(),
			Title:      "Task",
			WorkflowID: wfID,
			RepoID:     repoID,
			Label:      "work",
		}); err != nil {
			t.Fatalf("create task: %v", err)
		}
	}

	// Walk the full list in pages of 2, collecting ids and asserting no dupes.
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		q := "?limit=2"
		if cursor != "" {
			q += "&after=" + cursor
		}
		page, next := listTasksPage(t, r, q)
		pages++
		if len(page) > 2 {
			t.Fatalf("page returned %d tasks, expected <= 2", len(page))
		}
		for _, task := range page {
			if seen[task.ID] {
				t.Fatalf("task %s returned on more than one page", task.ID)
			}
			seen[task.ID] = true
		}
		if next == "" {
			break
		}
		cursor = next
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 5 {
		t.Fatalf("expected to page through all 5 tasks, saw %d", len(seen))
	}
	// 5 tasks / 2 per page = 3 pages, and the last full page must not advertise
	// a further cursor once the extra look-ahead row is exhausted.
	if _, next := listTasksPage(t, r, "?limit=5"); next != "" {
		t.Errorf("a full-size page covering every task should have no next cursor, got %q", next)
	}
}

func TestTasks_List_LimitCapped(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()
	// An over-max limit must be clamped rather than rejected.
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "work",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	page, _ := listTasksPage(t, r, "?limit=100000")
	if len(page) != 1 {
		t.Fatalf("expected the single task back, got %d", len(page))
	}
}

// seedRun creates an agent run with n log entries spaced one second apart and
// returns the run id.
func seedRun(t *testing.T, q *gen.Queries, taskID string, n int) string {
	t.Helper()
	ctx := context.Background()
	runID := uuid.NewString()
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:     runID,
		TaskID: taskID,
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		if err := q.CreateAgentLog(ctx, gen.CreateAgentLogParams{
			ID:         uuid.NewString(),
			AgentRunID: runID,
			Timestamp:  base.Add(time.Duration(i) * time.Second),
			Type:       "text",
			Content:    fmt.Sprintf("line %d", i),
		}); err != nil {
			t.Fatalf("create log: %v", err)
		}
	}
	return runID
}

// getLogsPage GETs a run's logs and returns the decoded entries plus the
// X-Has-More and X-Prev-Cursor headers.
func getLogsPage(t *testing.T, r http.Handler, taskID, runID, query string) ([]gen.AgentLog, bool, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/runs/"+runID+"/logs"+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET logs%s: expected 200, got %d: %s", query, w.Code, w.Body)
	}
	var logs []gen.AgentLog
	if err := json.NewDecoder(w.Body).Decode(&logs); err != nil {
		t.Fatal(err)
	}
	return logs, w.Header().Get("X-Has-More") == "true", w.Header().Get("X-Prev-Cursor")
}

func TestTasks_RunLogs_Pagination(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)
	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	runID := seedRun(t, q, task.ID, 5)

	// No cursor returns the newest page (tail), in chronological order.
	page, hasMore, prev := getLogsPage(t, r, task.ID, runID, "?limit=2")
	if len(page) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(page))
	}
	if page[0].Content != "line 3" || page[1].Content != "line 4" {
		t.Errorf("tail page should be the two newest in order, got %q,%q", page[0].Content, page[1].Content)
	}
	if !hasMore || prev == "" {
		t.Fatalf("expected has_more with a prev cursor, got hasMore=%v prev=%q", hasMore, prev)
	}
	if prev != page[0].ID {
		t.Errorf("prev cursor should be the oldest id in the page")
	}

	// Walk backwards to the start via the before cursor, collecting everything.
	seen := map[string]bool{}
	for _, l := range page {
		seen[l.ID] = true
	}
	cursor := prev
	for {
		older, more, p := getLogsPage(t, r, task.ID, runID, "?limit=2&before="+cursor)
		for i := 1; i < len(older); i++ {
			if older[i-1].Content >= older[i].Content {
				t.Errorf("page not in chronological order: %q then %q", older[i-1].Content, older[i].Content)
			}
		}
		for _, l := range older {
			if seen[l.ID] {
				t.Fatalf("log %s returned twice across pages", l.ID)
			}
			seen[l.ID] = true
		}
		if !more {
			break
		}
		cursor = p
	}
	if len(seen) != 5 {
		t.Fatalf("expected to page through all 5 log entries, saw %d", len(seen))
	}
}

// fakeReplyDispatcher records DispatchReply calls and returns a scripted result.
type fakeReplyDispatcher struct {
	calledTask string
	calledMsg  string
	runID      string
	err        error
}

func (f *fakeReplyDispatcher) DispatchReply(_ context.Context, taskID, message string) (string, error) {
	f.calledTask = taskID
	f.calledMsg = message
	if f.err != nil {
		return "", f.err
	}
	return f.runID, nil
}

// setupReplyRouter wires just the reply route against a caller-supplied dispatcher.
func setupReplyRouter(t *testing.T, disp handlers.ReplyDispatcher) (http.Handler, *gen.Queries, string, string) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), noopPub{})

	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID: repoID, Name: "test-repo", Path: t.TempDir(), WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	h := handlers.NewTasksHandler(q, engine, t.TempDir(), &fakeCanceller{found: map[string]bool{}}, disp)
	r := chi.NewRouter()
	r.Post("/tasks/{id}/runs/{run_id}/reply", h.ReplyRun)
	return r, q, wfID, repoID
}

// seedWaitingRun creates a task with an active waiting_human run.
func seedWaitingRun(t *testing.T, q *gen.Queries, wfID, repoID string) (taskID, runID string) {
	t.Helper()
	taskID, runID = seedRunningRun(t, q, wfID, repoID, "waiting_human")
	if err := q.SetTaskActiveRun(context.Background(), gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID, ActiveAgentRunID: &runID, ID: taskID,
	}); err != nil {
		t.Fatalf("set active run: %v", err)
	}
	return taskID, runID
}

func TestTasks_ReplyRun_OK(t *testing.T) {
	disp := &fakeReplyDispatcher{runID: "new-run"}
	r, q, wfID, repoID := setupReplyRouter(t, disp)
	taskID, runID := seedWaitingRun(t, q, wfID, repoID)

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/runs/"+runID+"/reply",
		strings.NewReader(`{"message":"  use approach B  "}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", w.Code, w.Body.String())
	}
	if disp.calledTask != taskID || disp.calledMsg != "use approach B" {
		t.Errorf("expected DispatchReply(%s, trimmed message), got (%s, %q)", taskID, disp.calledTask, disp.calledMsg)
	}
}

func TestTasks_ReplyRun_EmptyMessage(t *testing.T) {
	disp := &fakeReplyDispatcher{runID: "new-run"}
	r, q, wfID, repoID := setupReplyRouter(t, disp)
	taskID, runID := seedWaitingRun(t, q, wfID, repoID)

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/runs/"+runID+"/reply",
		strings.NewReader(`{"message":"   "}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if disp.calledTask != "" {
		t.Error("dispatcher must not be called for an empty message")
	}
}

func TestTasks_ReplyRun_NotActiveRun(t *testing.T) {
	disp := &fakeReplyDispatcher{runID: "new-run"}
	r, q, wfID, repoID := setupReplyRouter(t, disp)
	// waiting_human run exists but is not marked as the task's active run.
	taskID, runID := seedRunningRun(t, q, wfID, repoID, "waiting_human")

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/runs/"+runID+"/reply",
		strings.NewReader(`{"message":"hello"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestTasks_ReplyRun_WrongTask(t *testing.T) {
	disp := &fakeReplyDispatcher{runID: "new-run"}
	r, q, wfID, repoID := setupReplyRouter(t, disp)
	_, runID := seedWaitingRun(t, q, wfID, repoID)

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+uuid.NewString()+"/runs/"+runID+"/reply",
		strings.NewReader(`{"message":"hello"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestTasks_ReplyRun_DispatcherNotWaiting(t *testing.T) {
	disp := &fakeReplyDispatcher{err: agent.ErrRunNotWaiting}
	r, q, wfID, repoID := setupReplyRouter(t, disp)
	taskID, runID := seedWaitingRun(t, q, wfID, repoID)

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/runs/"+runID+"/reply",
		strings.NewReader(`{"message":"hello"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (%s)", w.Code, w.Body.String())
	}
}

// ---------- Write-back wiring (UpdateGitState) ----------

func TestUpdateGitState_PRMerged_TriggersWriteback(t *testing.T) {
	r, q, wfID, repoID, fwb := setupTaskRouterWithWriteback(t)

	remote := "https://github.com/acme/widgets"
	if _, err := q.UpdateRepo(context.Background(), gen.UpdateRepoParams{
		ID:                    repoID,
		Name:                  "test-repo",
		Path:                  t.TempDir(),
		RemoteUrl:             &remote,
		WorkflowID:            &wfID,
		IssueWritebackEnabled: 1,
	}); err != nil {
		t.Fatalf("update repo: %v", err)
	}

	task, err := q.CreateSourcedTask(context.Background(), gen.CreateSourcedTaskParams{
		ID:          uuid.NewString(),
		Title:       "Imported task",
		WorkflowID:  wfID,
		RepoID:      repoID,
		Label:       "work",
		Attachments: "[]",
		Source:      "github",
		SourceRef:   "acme/widgets#5",
	})
	if err != nil {
		t.Fatalf("create sourced task: %v", err)
	}
	if _, err := q.SetTaskPR(context.Background(), gen.SetTaskPRParams{
		GitState: "pr_open",
		PrUrl:    "https://github.com/acme/widgets/pull/5",
		ID:       task.ID,
	}); err != nil {
		t.Fatalf("set task pr: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID+"/git-state", strings.NewReader(`{"git_state":"pr_merged"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	if len(fwb.closeCalls) != 1 {
		t.Fatalf("expected 1 close-with-comment call, got %d: %v", len(fwb.closeCalls), fwb.closeCalls)
	}

	updated, err := q.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WritebackClosed == 0 {
		t.Error("expected writeback_closed to be set")
	}
}

func TestUpdateGitState_PRMerged_WritebackDisabled_NoOp(t *testing.T) {
	r, q, wfID, repoID, fwb := setupTaskRouterWithWriteback(t)

	// Write-back not enabled on this repo.
	task, err := q.CreateSourcedTask(context.Background(), gen.CreateSourcedTaskParams{
		ID:          uuid.NewString(),
		Title:       "Imported task",
		WorkflowID:  wfID,
		RepoID:      repoID,
		Label:       "work",
		Attachments: "[]",
		Source:      "github",
		SourceRef:   "acme/widgets#5",
	})
	if err != nil {
		t.Fatalf("create sourced task: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID+"/git-state", strings.NewReader(`{"git_state":"pr_merged"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	if len(fwb.closeCalls) != 0 {
		t.Fatalf("expected no close-with-comment calls, got %v", fwb.closeCalls)
	}
}

func TestUpdateGitState_NonMergedState_NoWriteback(t *testing.T) {
	r, q, wfID, repoID, fwb := setupTaskRouterWithWriteback(t)

	remote := "https://github.com/acme/widgets"
	if _, err := q.UpdateRepo(context.Background(), gen.UpdateRepoParams{
		ID:                    repoID,
		Name:                  "test-repo",
		Path:                  t.TempDir(),
		RemoteUrl:             &remote,
		WorkflowID:            &wfID,
		IssueWritebackEnabled: 1,
	}); err != nil {
		t.Fatalf("update repo: %v", err)
	}

	task, err := q.CreateSourcedTask(context.Background(), gen.CreateSourcedTaskParams{
		ID:          uuid.NewString(),
		Title:       "Imported task",
		WorkflowID:  wfID,
		RepoID:      repoID,
		Label:       "work",
		Attachments: "[]",
		Source:      "github",
		SourceRef:   "acme/widgets#5",
	})
	if err != nil {
		t.Fatalf("create sourced task: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+task.ID+"/git-state", strings.NewReader(`{"git_state":"pr_open"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	if len(fwb.closeCalls) != 0 {
		t.Fatalf("expected no close-with-comment calls for a non-merged state, got %v", fwb.closeCalls)
	}
}
