package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type apiTemplate struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

func setupTemplatesRouter(t *testing.T) http.Handler {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewTemplatesHandler(q)

	r := chi.NewRouter()
	r.Get("/templates", h.List)
	r.Post("/templates", h.Create)
	r.Get("/templates/{id}", h.Get)
	r.Put("/templates/{id}", h.Update)
	r.Delete("/templates/{id}", h.Delete)
	return r
}

func createTemplate(t *testing.T, r http.Handler, body map[string]string) apiTemplate {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/templates", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create template: expected 201, got %d: %s", w.Code, w.Body)
	}
	var tpl apiTemplate
	if err := json.NewDecoder(w.Body).Decode(&tpl); err != nil {
		t.Fatal(err)
	}
	return tpl
}

func TestTemplates_Create_And_List(t *testing.T) {
	r := setupTemplatesRouter(t)

	tpl := createTemplate(t, r, map[string]string{
		"name":        "Upgrade dependency",
		"title":       "Upgrade <package> to latest",
		"description": "Bump the version, run tests, note breaking changes.",
	})
	if tpl.Type != "feature" {
		t.Errorf("expected default type 'feature', got %q", tpl.Type)
	}

	req := httptest.NewRequest(http.MethodGet, "/templates", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var templates []apiTemplate
	_ = json.NewDecoder(w.Body).Decode(&templates)
	if len(templates) != 1 || templates[0].ID != tpl.ID {
		t.Errorf("expected the created template in the list, got %+v", templates)
	}
}

func TestTemplates_Create_MissingName_Returns400(t *testing.T) {
	r := setupTemplatesRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/templates", jsonBody(t, map[string]string{"title": "no name"}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestTemplates_Create_DuplicateName_Returns409(t *testing.T) {
	r := setupTemplatesRouter(t)
	createTemplate(t, r, map[string]string{"name": "dupe"})

	req := httptest.NewRequest(http.MethodPost, "/templates", jsonBody(t, map[string]string{"name": "dupe"}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

func TestTemplates_Update_OK(t *testing.T) {
	r := setupTemplatesRouter(t)
	tpl := createTemplate(t, r, map[string]string{"name": "flaky test", "type": "bug"})

	body := map[string]string{"name": "fix flaky test", "title": "Fix flaky <test>", "type": "bug"}
	req := httptest.NewRequest(http.MethodPut, "/templates/"+tpl.ID, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var updated apiTemplate
	_ = json.NewDecoder(w.Body).Decode(&updated)
	if updated.Name != "fix flaky test" || updated.Title != "Fix flaky <test>" {
		t.Errorf("update not applied: %+v", updated)
	}
}

func TestTemplates_Update_NotFound(t *testing.T) {
	r := setupTemplatesRouter(t)

	body := map[string]string{"name": "whatever"}
	req := httptest.NewRequest(http.MethodPut, "/templates/nope", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestTemplates_Delete_OK(t *testing.T) {
	r := setupTemplatesRouter(t)
	tpl := createTemplate(t, r, map[string]string{"name": "temp"})

	req := httptest.NewRequest(http.MethodDelete, "/templates/"+tpl.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/templates/"+tpl.ID, nil)
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", getW.Code)
	}
}
