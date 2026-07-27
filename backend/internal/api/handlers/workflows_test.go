package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	r.Put("/workflows/{id}", h.Update)
	r.Delete("/workflows/{id}", h.Delete)
	r.Get("/workflows/{id}/export.yaml", h.ExportWorkflowYAML)
	r.Put("/workflows/{id}/yaml", h.UpdateWorkflowYAML)
	r.Post("/workflows/import", h.ImportWorkflowYAML)

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

// ---------- wip_limit / wip_limit_hard round-trip ----------

func TestWorkflows_Update_JSON_WipLimitRoundTrips(t *testing.T) {
	r, q := setupWorkflowRouter(t)

	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID

	limit := int64(5)
	body := map[string]any{
		"name": "Updated",
		"labels": []map[string]any{
			{"name": "work", "color": "#111", "sort_order": 0, "wip_limit": nil, "wip_limit_hard": false},
			{"name": "review", "color": "#222", "sort_order": 1, "wip_limit": limit, "wip_limit_hard": true},
		},
	}
	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wfID, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}

	labels, err := q.ListWorkflowLabels(context.Background(), wfID)
	if err != nil {
		t.Fatal(err)
	}
	var work, review *gen.WorkflowLabel
	for i := range labels {
		switch labels[i].Name {
		case "work":
			work = &labels[i]
		case "review":
			review = &labels[i]
		}
	}
	if work == nil || review == nil {
		t.Fatalf("expected work and review labels, got %+v", labels)
	}
	if work.WipLimit != nil {
		t.Errorf("expected work.wip_limit nil, got %v", *work.WipLimit)
	}
	if work.WipLimitHard != 0 {
		t.Errorf("expected work.wip_limit_hard = 0, got %d", work.WipLimitHard)
	}
	if review.WipLimit == nil || *review.WipLimit != 5 {
		t.Errorf("expected review.wip_limit = 5, got %+v", review.WipLimit)
	}
	if review.WipLimitHard != 1 {
		t.Errorf("expected review.wip_limit_hard = 1, got %d", review.WipLimitHard)
	}
}

func TestWorkflows_YAML_WipLimitRoundTrips(t *testing.T) {
	r, _ := setupWorkflowRouter(t)

	yamlBody := `
name: yaml-wip-test
labels:
  - name: work
    color: "#111"
    sort_order: 0
  - name: review
    color: "#222"
    sort_order: 1
    wip_limit: 3
    wip_limit_hard: true
transitions:
  - from: work
    to: review
    trigger: agent
`
	req := httptest.NewRequest(http.MethodPost, "/workflows/import", strings.NewReader(yamlBody))
	req.Header.Set("Content-Type", "application/yaml")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body)
	}

	var created struct {
		gen.Workflow
		Labels []gen.WorkflowLabel `json:"labels"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	var review *gen.WorkflowLabel
	for i := range created.Labels {
		if created.Labels[i].Name == "review" {
			review = &created.Labels[i]
		}
	}
	if review == nil {
		t.Fatalf("expected review label in import response, got %+v", created.Labels)
	}
	if review.WipLimit == nil || *review.WipLimit != 3 {
		t.Errorf("expected wip_limit 3 after import, got %+v", review.WipLimit)
	}
	if review.WipLimitHard != 1 {
		t.Errorf("expected wip_limit_hard = 1 after import, got %d", review.WipLimitHard)
	}

	// Export and check the YAML round-trips the fields back out.
	exportReq := httptest.NewRequest(http.MethodGet, "/workflows/"+created.ID+"/export.yaml", nil)
	exportW := httptest.NewRecorder()
	r.ServeHTTP(exportW, exportReq)
	if exportW.Code != http.StatusOK {
		t.Fatalf("expected 200 exporting, got %d: %s", exportW.Code, exportW.Body)
	}
	exported := exportW.Body.String()
	if !strings.Contains(exported, "wip_limit: 3") {
		t.Errorf("expected exported yaml to contain wip_limit: 3, got:\n%s", exported)
	}
	if !strings.Contains(exported, "wip_limit_hard: true") {
		t.Errorf("expected exported yaml to contain wip_limit_hard: true, got:\n%s", exported)
	}
}

func TestWorkflows_YAML_WipLimitRejectsNonPositive(t *testing.T) {
	r, _ := setupWorkflowRouter(t)

	yamlBody := `
name: yaml-wip-invalid
labels:
  - name: work
    color: "#111"
    sort_order: 0
    wip_limit: 0
`
	req := httptest.NewRequest(http.MethodPost, "/workflows/import", strings.NewReader(yamlBody))
	req.Header.Set("Content-Type", "application/yaml")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for wip_limit: 0, got %d: %s", w.Code, w.Body)
	}
}
