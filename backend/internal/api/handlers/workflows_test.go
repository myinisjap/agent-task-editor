package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestWorkflows_List_Pagination verifies limit/after cursor pagination walks
// the full workflow list (including the seeded default) without dupes or
// gaps.
func TestWorkflows_List_Pagination(t *testing.T) {
	r, _ := setupWorkflowRouter(t)

	// Create 4 more workflows on top of the seeded default (5 total).
	for i := 0; i < 4; i++ {
		body := map[string]string{"name": fmt.Sprintf("wf-%d", i)}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/workflows", jsonBody(t, body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create workflow: %d %s", w.Code, w.Body.String())
		}
	}

	type wfID struct {
		ID string `json:"id"`
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		qs := "?limit=2"
		if cursor != "" {
			qs += "&after=" + cursor
		}
		req := httptest.NewRequest(http.MethodGet, "/workflows"+qs, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET workflows%s: expected 200, got %d: %s", qs, w.Code, w.Body)
		}
		var page []wfID
		if err := json.NewDecoder(w.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page) > 2 {
			t.Fatalf("page returned %d workflows, expected <= 2", len(page))
		}
		for _, wf := range page {
			if seen[wf.ID] {
				t.Fatalf("workflow %s returned on more than one page", wf.ID)
			}
			seen[wf.ID] = true
		}
		next := w.Header().Get("X-Next-Cursor")
		if next == "" {
			break
		}
		cursor = next
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 5 {
		t.Fatalf("expected to page through all 5 workflows, saw %d", len(seen))
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

func TestWorkflows_Create_DuplicateName_Returns400(t *testing.T) {
	r, _ := setupWorkflowRouter(t)

	body := map[string]string{"name": "Dupe Workflow"}
	req := httptest.NewRequest(http.MethodPost, "/workflows", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d: %s", w.Code, w.Body)
	}

	req = httptest.NewRequest(http.MethodPost, "/workflows", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("duplicate create: expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestWorkflows_Update_DuplicateNameRejected(t *testing.T) {
	r, _ := setupWorkflowRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/workflows", jsonBody(t, map[string]string{"name": "Existing Name"}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed workflow: expected 201, got %d: %s", w.Code, w.Body)
	}

	req = httptest.NewRequest(http.MethodPost, "/workflows", jsonBody(t, map[string]string{"name": "To Rename"}))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var toRename gen.Workflow
	if err := json.NewDecoder(w.Body).Decode(&toRename); err != nil {
		t.Fatalf("decode seed workflow: %v", err)
	}

	req = httptest.NewRequest(http.MethodPut, "/workflows/"+toRename.ID,
		jsonBody(t, map[string]any{"name": "Existing Name"}))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("rename to existing name: expected 400, got %d: %s", w.Code, w.Body)
	}
}

// TestWorkflows_Update_OmittedNameAndDescriptionPreserved verifies that a
// PUT body omitting name/description keeps the existing values instead of
// blanking them to "" — an empty name would break
// GetWorkflowByName("Default"), which resolveDefaultWorkflowID depends on
// for task creation.
func TestWorkflows_Update_OmittedNameAndDescriptionPreserved(t *testing.T) {
	r, q := setupWorkflowRouter(t)
	ctx := context.Background()

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{
		ID: "wf-omit-fields", Name: "Keep Me", Description: "keep this too",
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID, jsonBody(t, map[string]any{
		"labels":      []map[string]any{{"name": "work", "color": "#111", "sort_order": 0}},
		"transitions": []map[string]any{},
	}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}

	updated, err := q.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	if updated.Name != "Keep Me" {
		t.Errorf("expected name to be preserved as 'Keep Me', got %q", updated.Name)
	}
	if updated.Description != "keep this too" {
		t.Errorf("expected description to be preserved, got %q", updated.Description)
	}
}

// TestWorkflows_Update_RenameRollsBackOnTransitionFailure verifies that if
// replacing transitions fails partway through (here: an invalid
// trigger_type, rejected by the CHECK constraint), the workflow rename is
// rolled back along with everything else in the transaction rather than
// being silently committed against a DB state the client never asked for.
func TestWorkflows_Update_RenameRollsBackOnTransitionFailure(t *testing.T) {
	r, q := setupWorkflowRouter(t)
	ctx := context.Background()

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{
		ID: "wf-rollback", Name: "Original Name", Description: "orig",
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID, jsonBody(t, map[string]any{
		"name":   "New Name",
		"labels": []map[string]any{{"name": "work", "color": "#111", "sort_order": 0}},
		"transitions": []map[string]any{
			{"from_label": "work", "to_label": "done", "trigger_type": "not-a-real-type"},
		},
	}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from the CHECK constraint failure, got %d: %s", w.Code, w.Body)
	}

	after, err := q.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	if after.Name != "Original Name" {
		t.Errorf("expected rename to roll back with the rest of the transaction, but name is now %q", after.Name)
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

// TestWorkflows_Delete_NotFound verifies deleting an id that doesn't exist
// returns 404 rather than a false-positive 204.
func TestWorkflows_Delete_NotFound(t *testing.T) {
	r, _ := setupWorkflowRouter(t)

	req := httptest.NewRequest(http.MethodDelete, "/workflows/ghost", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body)
	}
}

// TestWorkflows_Delete_ConflictWhenTasksReference verifies deleting a
// workflow that still has tasks pointing at it returns 409 with the
// referencing task count instead of a raw 500 "FOREIGN KEY constraint
// failed" from the DB.
func TestWorkflows_Delete_ConflictWhenTasksReference(t *testing.T) {
	r, q := setupWorkflowRouter(t)
	ctx := context.Background()

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: "wf-in-use", Name: "In Use"})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	repoID := "repo-in-use"
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: t.TempDir(), WorkflowID: &wf.ID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: "task-in-use", Title: "t", WorkflowID: wf.ID, RepoID: repoID, Label: "work",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/workflows/"+wf.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body)
	}

	// The workflow must still exist — the delete must not have partially
	// applied.
	if _, err := q.GetWorkflow(ctx, wf.ID); err != nil {
		t.Errorf("expected workflow to still exist after conflicting delete: %v", err)
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

// ---------- UpdateWorkflowYAML ----------

func TestWorkflows_UpdateYAML_ReplacesLabelsAndTransitions(t *testing.T) {
	r, q := setupWorkflowRouter(t)

	wf, err := q.CreateWorkflow(context.Background(), gen.CreateWorkflowParams{
		ID: "wf-yaml-update", Name: "original-name", Description: "orig",
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	yamlBody := `
name: updated-name
description: updated description
labels:
  - name: alpha
    color: "#111"
    sort_order: 0
  - name: beta
    color: "#222"
    sort_order: 1
    is_terminal: true
transitions:
  - from: alpha
    to: beta
    trigger: agent
`
	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID+"/yaml", strings.NewReader(yamlBody))
	req.Header.Set("Content-Type", "application/yaml")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}

	var updated struct {
		gen.Workflow
		Labels      []gen.WorkflowLabel      `json:"labels"`
		Transitions []gen.WorkflowTransition `json:"transitions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Name != "updated-name" || updated.Description != "updated description" {
		t.Errorf("expected updated name/description, got %+v", updated.Workflow)
	}
	if len(updated.Labels) != 2 {
		t.Fatalf("expected 2 labels after update, got %d: %+v", len(updated.Labels), updated.Labels)
	}
	if len(updated.Transitions) != 1 {
		t.Fatalf("expected 1 transition after update, got %d: %+v", len(updated.Transitions), updated.Transitions)
	}

	labels, err := q.ListWorkflowLabels(context.Background(), wf.ID)
	if err != nil {
		t.Fatalf("list labels: %v", err)
	}
	if len(labels) != 2 {
		t.Errorf("expected DB to have exactly 2 labels after replace, got %d", len(labels))
	}
}

func TestWorkflows_UpdateYAML_InvalidYAML(t *testing.T) {
	r, q := setupWorkflowRouter(t)
	wf, err := q.CreateWorkflow(context.Background(), gen.CreateWorkflowParams{ID: "wf-bad-yaml", Name: "wf"})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID+"/yaml", strings.NewReader("not: [valid"))
	req.Header.Set("Content-Type", "application/yaml")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestWorkflows_UpdateYAML_MissingName(t *testing.T) {
	r, q := setupWorkflowRouter(t)
	wf, err := q.CreateWorkflow(context.Background(), gen.CreateWorkflowParams{ID: "wf-no-name", Name: "wf"})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID+"/yaml", strings.NewReader("labels: []\n"))
	req.Header.Set("Content-Type", "application/yaml")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestWorkflows_UpdateYAML_DuplicateNameRejected(t *testing.T) {
	r, q := setupWorkflowRouter(t)
	if _, err := q.CreateWorkflow(context.Background(), gen.CreateWorkflowParams{ID: "wf-existing", Name: "taken-name"}); err != nil {
		t.Fatalf("create workflow 1: %v", err)
	}
	wf2, err := q.CreateWorkflow(context.Background(), gen.CreateWorkflowParams{ID: "wf-other", Name: "other-name"})
	if err != nil {
		t.Fatalf("create workflow 2: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf2.ID+"/yaml", strings.NewReader("name: taken-name\nlabels: []\n"))
	req.Header.Set("Content-Type", "application/yaml")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestWorkflows_UpdateYAML_UnknownWorkflow(t *testing.T) {
	r, _ := setupWorkflowRouter(t)

	req := httptest.NewRequest(http.MethodPut, "/workflows/does-not-exist/yaml", strings.NewReader("name: whatever\nlabels: []\n"))
	req.Header.Set("Content-Type", "application/yaml")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body)
	}
}
