package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func setupWorkflowRouter(t *testing.T) (http.Handler, *gen.Queries) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewWorkflowsHandler(q, db.SQL())

	r := chi.NewRouter()
	r.Get("/workflows", h.List)
	r.Post("/workflows", h.Create)
	r.Get("/workflows/{id}", h.Get)
	r.Delete("/workflows/{id}", h.Delete)

	return r, q
}

func TestWorkflows_List_ContainsSeeded(t *testing.T) {
	r, _ := setupWorkflowRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/workflows", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// SeedDefaultWorkflow inserts one workflow; list must contain it
	var wfs []json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&wfs); err != nil {
		t.Fatal(err)
	}
	if len(wfs) == 0 {
		t.Error("expected at least the seeded default workflow")
	}
}

func TestWorkflows_Create_OK(t *testing.T) {
	r, _ := setupWorkflowRouter(t)

	body := map[string]string{"name": "My Workflow", "description": "for tests"}
	req := httptest.NewRequest(http.MethodPost, "/workflows", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body)
	}
	var wf gen.Workflow
	if err := json.NewDecoder(w.Body).Decode(&wf); err != nil {
		t.Fatal(err)
	}
	if wf.Name != "My Workflow" {
		t.Errorf("expected name 'My Workflow', got %q", wf.Name)
	}
}

func TestWorkflows_Create_MissingName_Returns400(t *testing.T) {
	r, _ := setupWorkflowRouter(t)

	body := map[string]string{"description": "no name"}
	req := httptest.NewRequest(http.MethodPost, "/workflows", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestWorkflows_Get_Found(t *testing.T) {
	r, q := setupWorkflowRouter(t)

	// Use the seeded workflow
	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID

	req := httptest.NewRequest(http.MethodGet, "/workflows/"+wfID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestWorkflows_Get_NotFound(t *testing.T) {
	r, _ := setupWorkflowRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/workflows/ghost", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestWorkflows_Delete_OK(t *testing.T) {
	r, q := setupWorkflowRouter(t)

	// Create a second workflow to delete (seeded one is referenced by tasks)
	wf, _ := q.CreateWorkflow(context.Background(), gen.CreateWorkflowParams{
		ID:   "wf-to-delete",
		Name: "Temp",
	})

	req := httptest.NewRequest(http.MethodDelete, "/workflows/"+wf.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}
