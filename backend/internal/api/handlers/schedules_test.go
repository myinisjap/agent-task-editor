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

type apiSchedule struct {
	ID          string  `json:"id"`
	TemplateID  string  `json:"template_id"`
	RepoID      string  `json:"repo_id"`
	CronExpr    string  `json:"cron_expr"`
	TargetLabel string  `json:"target_label"`
	Enabled     bool    `json:"enabled"`
	LastRunAt   *string `json:"last_run_at"`
}

// setupSchedulesRouter returns a router along with a pre-created template id
// and repo id to reference from schedule bodies.
func setupSchedulesRouter(t *testing.T) (http.Handler, string, string) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())

	wfs, err := q.ListWorkflows(context.Background())
	if err != nil || len(wfs) == 0 {
		t.Fatalf("expected seeded default workflow, err=%v wfs=%v", err, wfs)
	}
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

	tmpl, err := q.CreateTaskTemplate(context.Background(), gen.CreateTaskTemplateParams{
		ID:          uuid.NewString(),
		Name:        "tmpl",
		Title:       "Upgrade dependencies",
		Description: "Run the upgrade script.",
		Type:        "chore",
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	h := handlers.NewSchedulesHandler(q)
	r := chi.NewRouter()
	r.Get("/schedules", h.List)
	r.Post("/schedules", h.Create)
	r.Get("/schedules/{id}", h.Get)
	r.Put("/schedules/{id}", h.Update)
	r.Delete("/schedules/{id}", h.Delete)
	return r, tmpl.ID, repoID
}

func createSchedule(t *testing.T, r http.Handler, body map[string]any) (apiSchedule, int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/schedules", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var sched apiSchedule
	if w.Code == http.StatusCreated {
		if err := json.NewDecoder(w.Body).Decode(&sched); err != nil {
			t.Fatal(err)
		}
	}
	return sched, w.Code
}

func TestSchedules_Create_And_List(t *testing.T) {
	r, tmplID, repoID := setupSchedulesRouter(t)

	sched, code := createSchedule(t, r, map[string]any{
		"template_id": tmplID,
		"repo_id":     repoID,
		"cron_expr":   "0 6 * * 1",
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", code)
	}
	if sched.TargetLabel != "not_ready" {
		t.Errorf("expected default target_label 'not_ready', got %q", sched.TargetLabel)
	}
	if !sched.Enabled {
		t.Errorf("expected default enabled=true")
	}

	req := httptest.NewRequest(http.MethodGet, "/schedules", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var schedules []apiSchedule
	_ = json.NewDecoder(w.Body).Decode(&schedules)
	if len(schedules) != 1 || schedules[0].ID != sched.ID {
		t.Errorf("expected created schedule in list, got %+v", schedules)
	}
}

func TestSchedules_Create_MissingTemplateID_Returns400(t *testing.T) {
	r, _, repoID := setupSchedulesRouter(t)
	_, code := createSchedule(t, r, map[string]any{
		"repo_id":   repoID,
		"cron_expr": "0 6 * * 1",
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestSchedules_Create_MissingRepoID_Returns400(t *testing.T) {
	r, tmplID, _ := setupSchedulesRouter(t)
	_, code := createSchedule(t, r, map[string]any{
		"template_id": tmplID,
		"cron_expr":   "0 6 * * 1",
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestSchedules_Create_UnknownTemplate_Returns404(t *testing.T) {
	r, _, repoID := setupSchedulesRouter(t)
	_, code := createSchedule(t, r, map[string]any{
		"template_id": "does-not-exist",
		"repo_id":     repoID,
		"cron_expr":   "0 6 * * 1",
	})
	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

func TestSchedules_Create_UnknownRepo_Returns404(t *testing.T) {
	r, tmplID, _ := setupSchedulesRouter(t)
	_, code := createSchedule(t, r, map[string]any{
		"template_id": tmplID,
		"repo_id":     "does-not-exist",
		"cron_expr":   "0 6 * * 1",
	})
	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

func TestSchedules_Create_InvalidCron_Returns400(t *testing.T) {
	r, tmplID, repoID := setupSchedulesRouter(t)
	_, code := createSchedule(t, r, map[string]any{
		"template_id": tmplID,
		"repo_id":     repoID,
		"cron_expr":   "not a cron",
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestSchedules_Create_UnknownTargetLabel_Returns400(t *testing.T) {
	r, tmplID, repoID := setupSchedulesRouter(t)
	_, code := createSchedule(t, r, map[string]any{
		"template_id":  tmplID,
		"repo_id":      repoID,
		"cron_expr":    "0 6 * * 1",
		"target_label": "not-a-real-label",
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestSchedules_Create_ValidTargetLabel_Returns201(t *testing.T) {
	r, tmplID, repoID := setupSchedulesRouter(t)
	sched, code := createSchedule(t, r, map[string]any{
		"template_id":  tmplID,
		"repo_id":      repoID,
		"cron_expr":    "0 6 * * 1",
		"target_label": "work",
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", code)
	}
	if sched.TargetLabel != "work" {
		t.Errorf("target_label = %q, want work", sched.TargetLabel)
	}
}

func TestSchedules_Create_RepoWithoutWorkflow_Returns400(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:   repoID,
		Name: "no-workflow-repo",
		Path: t.TempDir(),
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	tmpl, err := q.CreateTaskTemplate(context.Background(), gen.CreateTaskTemplateParams{
		ID:          uuid.NewString(),
		Name:        "tmpl2",
		Title:       "Upgrade dependencies",
		Description: "Run the upgrade script.",
		Type:        "chore",
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	h := handlers.NewSchedulesHandler(q)
	r := chi.NewRouter()
	r.Post("/schedules", h.Create)

	_, code := createSchedule(t, r, map[string]any{
		"template_id": tmpl.ID,
		"repo_id":     repoID,
		"cron_expr":   "0 6 * * 1",
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestSchedules_Update_UnknownTargetLabel_Returns400(t *testing.T) {
	r, tmplID, repoID := setupSchedulesRouter(t)
	sched, _ := createSchedule(t, r, map[string]any{
		"template_id": tmplID,
		"repo_id":     repoID,
		"cron_expr":   "0 6 * * 1",
	})

	body := map[string]any{
		"cron_expr":    "0 * * * *",
		"target_label": "not-a-real-label",
	}
	req := httptest.NewRequest(http.MethodPut, "/schedules/"+sched.ID, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestSchedules_Get(t *testing.T) {
	r, tmplID, repoID := setupSchedulesRouter(t)
	sched, _ := createSchedule(t, r, map[string]any{
		"template_id": tmplID,
		"repo_id":     repoID,
		"cron_expr":   "0 6 * * 1",
	})

	req := httptest.NewRequest(http.MethodGet, "/schedules/"+sched.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSchedules_Get_NotFound(t *testing.T) {
	r, _, _ := setupSchedulesRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/schedules/nope", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSchedules_Update(t *testing.T) {
	r, tmplID, repoID := setupSchedulesRouter(t)
	sched, _ := createSchedule(t, r, map[string]any{
		"template_id": tmplID,
		"repo_id":     repoID,
		"cron_expr":   "0 6 * * 1",
	})

	body := map[string]any{
		"cron_expr":    "0 * * * *",
		"target_label": "work",
		"enabled":      false,
	}
	req := httptest.NewRequest(http.MethodPut, "/schedules/"+sched.ID, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var updated apiSchedule
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.CronExpr != "0 * * * *" {
		t.Errorf("cron_expr = %q, want %q", updated.CronExpr, "0 * * * *")
	}
	if updated.TargetLabel != "work" {
		t.Errorf("target_label = %q, want work", updated.TargetLabel)
	}
	if updated.Enabled {
		t.Errorf("expected enabled=false after update")
	}
}

func TestSchedules_Update_NotFound(t *testing.T) {
	r, _, _ := setupSchedulesRouter(t)
	body := map[string]any{"cron_expr": "0 * * * *"}
	req := httptest.NewRequest(http.MethodPut, "/schedules/nope", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSchedules_Update_InvalidCron_Returns400(t *testing.T) {
	r, tmplID, repoID := setupSchedulesRouter(t)
	sched, _ := createSchedule(t, r, map[string]any{
		"template_id": tmplID,
		"repo_id":     repoID,
		"cron_expr":   "0 6 * * 1",
	})

	body := map[string]any{"cron_expr": "nope"}
	req := httptest.NewRequest(http.MethodPut, "/schedules/"+sched.ID, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSchedules_Delete(t *testing.T) {
	r, tmplID, repoID := setupSchedulesRouter(t)
	sched, _ := createSchedule(t, r, map[string]any{
		"template_id": tmplID,
		"repo_id":     repoID,
		"cron_expr":   "0 6 * * 1",
	})

	req := httptest.NewRequest(http.MethodDelete, "/schedules/"+sched.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/schedules/"+sched.ID, nil)
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", getW.Code)
	}
}
