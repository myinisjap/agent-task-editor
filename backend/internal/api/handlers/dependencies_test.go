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
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// setupDepRouter wires the tasks + dependencies routes against a fresh DB and
// returns the router, queries, workflow id, and repo id.
func setupDepRouter(t *testing.T) (http.Handler, *storage.DB, *gen.Queries, string, string) {
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

	tasksH := handlers.NewTasksHandler(q, engine, t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil)
	depsH := handlers.NewDependenciesHandler(q, db.SQL(), noopPub{})

	r := chi.NewRouter()
	r.Get("/tasks", tasksH.List)
	r.Get("/tasks/{id}", tasksH.Get)
	r.Patch("/tasks/{id}/label", tasksH.MoveLabel)
	r.Get("/tasks/{id}/dependencies", depsH.List)
	r.Post("/tasks/{id}/dependencies", depsH.Add)
	r.Delete("/tasks/{id}/dependencies/{dep_id}", depsH.Remove)
	return r, db, q, wfID, repoID
}

func mkTask(t *testing.T, q *gen.Queries, wfID, repoID, title, label string) gen.Task {
	t.Helper()
	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: title, WorkflowID: wfID, RepoID: repoID, Label: label,
	})
	if err != nil {
		t.Fatalf("create task %s: %v", title, err)
	}
	return task
}

func addDep(t *testing.T, r http.Handler, taskID, dependsOn string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/dependencies",
		jsonBody(t, map[string]string{"depends_on_task_id": dependsOn}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestDependencies_DispatchGate verifies the pickup query skips a task with an
// unsatisfied blocker and includes it once the blocker reaches a terminal label.
func TestDependencies_DispatchGate(t *testing.T) {
	r, _, q, wfID, repoID := setupDepRouter(t)
	ctx := context.Background()

	blocker := mkTask(t, q, wfID, repoID, "blocker", "work")
	dependent := mkTask(t, q, wfID, repoID, "dependent", "work")

	if w := addDep(t, r, dependent.ID, blocker.ID); w.Code != http.StatusNoContent {
		t.Fatalf("add dep: got %d, body %s", w.Code, w.Body.String())
	}

	pickup, err := q.ListAgentPickupTasks(ctx)
	if err != nil {
		t.Fatalf("pickup: %v", err)
	}
	if containsTask(pickup, dependent.ID) {
		t.Fatalf("dependent should be gated out while blocker is unsatisfied")
	}
	if !containsTask(pickup, blocker.ID) {
		t.Fatalf("blocker should be pickup-eligible")
	}

	// Move blocker to a terminal label; dependent becomes eligible.
	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{Label: "done", ID: blocker.ID}); err != nil {
		t.Fatalf("terminal move: %v", err)
	}
	pickup, _ = q.ListAgentPickupTasks(ctx)
	if !containsTask(pickup, dependent.ID) {
		t.Fatalf("dependent should be eligible once blocker is terminal")
	}
}

// TestDependencies_ArchivedBlockerSatisfies verifies archiving a blocker
// satisfies the edge (avoids invisible deadlocks).
func TestDependencies_ArchivedBlockerSatisfies(t *testing.T) {
	r, _, q, wfID, repoID := setupDepRouter(t)
	ctx := context.Background()

	blocker := mkTask(t, q, wfID, repoID, "blocker", "work")
	dependent := mkTask(t, q, wfID, repoID, "dependent", "work")
	if w := addDep(t, r, dependent.ID, blocker.ID); w.Code != http.StatusNoContent {
		t.Fatalf("add dep: %d", w.Code)
	}
	if _, err := q.SetTaskArchived(ctx, gen.SetTaskArchivedParams{Archived: 1, ID: blocker.ID}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	pickup, _ := q.ListAgentPickupTasks(ctx)
	if !containsTask(pickup, dependent.ID) {
		t.Fatalf("archiving a blocker should satisfy the edge")
	}
}

// TestDependencies_Cycle_Rejected verifies a cycle-closing edge returns 409.
func TestDependencies_Cycle_Rejected(t *testing.T) {
	r, _, q, wfID, repoID := setupDepRouter(t)
	a := mkTask(t, q, wfID, repoID, "A", "work")
	b := mkTask(t, q, wfID, repoID, "B", "work")

	if w := addDep(t, r, a.ID, b.ID); w.Code != http.StatusNoContent {
		t.Fatalf("A depends on B: %d %s", w.Code, w.Body.String())
	}
	// B depends on A would close A -> B -> A.
	w := addDep(t, r, b.ID, a.ID)
	if w.Code != http.StatusConflict {
		t.Fatalf("cycle should be 409, got %d %s", w.Code, w.Body.String())
	}
}

// TestDependencies_SelfEdge_Rejected verifies self-dependencies are refused.
func TestDependencies_SelfEdge_Rejected(t *testing.T) {
	r, _, q, wfID, repoID := setupDepRouter(t)
	a := mkTask(t, q, wfID, repoID, "A", "work")
	w := addDep(t, r, a.ID, a.ID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("self-edge should be 400, got %d", w.Code)
	}
}

// TestDependencies_Duplicate_Rejected verifies a repeated edge returns 409.
func TestDependencies_Duplicate_Rejected(t *testing.T) {
	r, _, q, wfID, repoID := setupDepRouter(t)
	a := mkTask(t, q, wfID, repoID, "A", "work")
	b := mkTask(t, q, wfID, repoID, "B", "work")
	if w := addDep(t, r, a.ID, b.ID); w.Code != http.StatusNoContent {
		t.Fatalf("first add: %d", w.Code)
	}
	if w := addDep(t, r, a.ID, b.ID); w.Code != http.StatusConflict {
		t.Fatalf("duplicate should be 409, got %d", w.Code)
	}
}

// TestDependencies_List_And_Counts verifies the list endpoint reports blockers
// with satisfaction state and that GET /tasks/{id} carries derived counts.
func TestDependencies_List_And_Counts(t *testing.T) {
	r, _, q, wfID, repoID := setupDepRouter(t)
	blocker := mkTask(t, q, wfID, repoID, "blocker", "work")
	dependent := mkTask(t, q, wfID, repoID, "dependent", "work")
	if w := addDep(t, r, dependent.ID, blocker.ID); w.Code != http.StatusNoContent {
		t.Fatalf("add: %d", w.Code)
	}

	// List the dependent's edges: one unmet blocker.
	req := httptest.NewRequest(http.MethodGet, "/tasks/"+dependent.ID+"/dependencies", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var listResp struct {
		BlockedBy []struct {
			TaskID    string `json:"task_id"`
			Satisfied bool   `json:"satisfied"`
		} `json:"blocked_by"`
		BlockedByCount int `json:"blocked_by_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp.BlockedByCount != 1 || len(listResp.BlockedBy) != 1 || listResp.BlockedBy[0].Satisfied {
		t.Fatalf("expected one unmet blocker, got %+v", listResp)
	}

	// GET the dependent task: derived count present.
	req = httptest.NewRequest(http.MethodGet, "/tasks/"+dependent.ID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var task struct {
		BlockedByCount int `json:"blocked_by_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if task.BlockedByCount != 1 {
		t.Fatalf("expected blocked_by_count 1, got %d", task.BlockedByCount)
	}

	// Remove the edge; count clears.
	req = httptest.NewRequest(http.MethodDelete, "/tasks/"+dependent.ID+"/dependencies/"+blocker.ID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	pickup, _ := q.ListAgentPickupTasks(context.Background())
	if !containsTask(pickup, dependent.ID) {
		t.Fatalf("removing the edge should unblock the dependent")
	}
}

// TestDependencies_CrossWorkflow_Rejected verifies edges can't span workflows.
func TestDependencies_CrossWorkflow_Rejected(t *testing.T) {
	r, _, q, wfID, repoID := setupDepRouter(t)
	ctx := context.Background()

	// Second workflow with a terminal label.
	wf2 := uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wf2, Name: "Other", Description: ""}); err != nil {
		t.Fatalf("create wf2: %v", err)
	}
	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wf2, Name: "done", Color: "#000", SortOrder: 0, IsTerminal: 1,
	}); err != nil {
		t.Fatalf("create wf2 label: %v", err)
	}
	repo2 := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repo2, Name: "r2", Path: t.TempDir(), WorkflowID: &wf2}); err != nil {
		t.Fatalf("create repo2: %v", err)
	}

	a := mkTask(t, q, wfID, repoID, "A", "work")
	other := mkTask(t, q, wf2, repo2, "Other", "done")
	w := addDep(t, r, a.ID, other.ID)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("cross-workflow edge should be 400, got %d %s", w.Code, w.Body.String())
	}
}

func containsTask(tasks []gen.Task, id string) bool {
	for _, t := range tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}
