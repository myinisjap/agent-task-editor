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

type apiIntakeRule struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Enabled          bool     `json:"enabled"`
	SortOrder        int64    `json:"sort_order"`
	MatchSource      string   `json:"match_source"`
	MatchLabels      []string `json:"match_labels"`
	MatchAuthorAssoc []string `json:"match_author_assoc"`
	ApplyTargetLabel string   `json:"apply_target_label"`
	ApplyPriority    *int64   `json:"apply_priority"`
}

// setupIntakeRulesRouter returns a router along with a pre-created repo id
// (default workflow, with "not_ready" as the gate and "work" as an
// agent-triggerable label) to reference from rule bodies.
func setupIntakeRulesRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	r, _, repoID := setupIntakeRulesRouterWithQueries(t)
	return r, repoID
}

// setupIntakeRulesRouterWithQueries is setupIntakeRulesRouter plus the
// underlying *gen.Queries, for tests that need to seed additional fixtures
// (e.g. a task template) against the same database the router uses.
func setupIntakeRulesRouterWithQueries(t *testing.T) (http.Handler, *gen.Queries, string) {
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

	h := handlers.NewIntakeRulesHandler(q)
	r := chi.NewRouter()
	r.Get("/intake-rules", h.List)
	r.Post("/intake-rules", h.Create)
	r.Post("/intake-rules/preview", h.Preview)
	r.Get("/intake-rules/{id}", h.Get)
	r.Put("/intake-rules/{id}", h.Update)
	r.Delete("/intake-rules/{id}", h.Delete)
	return r, q, repoID
}

func createIntakeRule(t *testing.T, r http.Handler, body map[string]any) (apiIntakeRule, int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/intake-rules", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var rule apiIntakeRule
	if w.Code == http.StatusCreated {
		if err := json.NewDecoder(w.Body).Decode(&rule); err != nil {
			t.Fatal(err)
		}
	}
	return rule, w.Code
}

func TestIntakeRules_Create_And_List(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)

	rule, code := createIntakeRule(t, r, map[string]any{
		"name":         "bug triage",
		"match_source": "issue",
		"match_labels": []string{"bug"},
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", code)
	}
	if !rule.Enabled {
		t.Error("expected default enabled=true")
	}

	req := httptest.NewRequest(http.MethodGet, "/intake-rules", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var rules []apiIntakeRule
	_ = json.NewDecoder(w.Body).Decode(&rules)
	if len(rules) != 1 || rules[0].ID != rule.ID {
		t.Errorf("expected created rule in list, got %+v", rules)
	}
}

func TestIntakeRules_Create_MissingName_Returns400(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	_, code := createIntakeRule(t, r, map[string]any{
		"match_source": "issue",
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestIntakeRules_Create_InvalidMatchSource_Returns400(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	_, code := createIntakeRule(t, r, map[string]any{
		"name":         "bad source",
		"match_source": "not-a-real-source",
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestIntakeRules_Create_InvalidRegexp_Returns400(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	_, code := createIntakeRule(t, r, map[string]any{
		"name":                "bad regexp",
		"match_title_pattern": "(unclosed",
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestIntakeRules_Create_InvalidPriority_Returns400(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	prio := int64(99)
	_, code := createIntakeRule(t, r, map[string]any{
		"name":           "bad priority",
		"apply_priority": prio,
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestIntakeRules_Create_NegativeMaxCost_Returns400(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	_, code := createIntakeRule(t, r, map[string]any{
		"name":               "bad cost",
		"apply_max_cost_usd": -5.0,
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestIntakeRules_Create_UnknownRepo_Returns404(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	_, code := createIntakeRule(t, r, map[string]any{
		"name":          "unknown repo",
		"match_repo_id": "does-not-exist",
	})
	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

func TestIntakeRules_Create_UnknownTemplate_Returns404(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	_, code := createIntakeRule(t, r, map[string]any{
		"name":              "unknown template",
		"apply_template_id": "does-not-exist",
	})
	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

// TestIntakeRules_Create_AutoStartWithoutTrustedAuthor_Returns400 is the
// single most important validation in this handler: a rule that would land
// a task on an agent-triggerable label ("work", not the gate "not_ready")
// without restricting match_author_assoc to trusted associations must be
// rejected outright — see intake.AutoStartAllowed.
func TestIntakeRules_Create_AutoStartWithoutTrustedAuthor_Returns400(t *testing.T) {
	r, repoID := setupIntakeRulesRouter(t)
	_, code := createIntakeRule(t, r, map[string]any{
		"name":               "unsafe auto-start",
		"match_repo_id":      repoID,
		"apply_target_label": "work",
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 for auto-start without trusted author constraint, got %d", code)
	}
}

func TestIntakeRules_Create_AutoStartWithMixedTrust_Returns400(t *testing.T) {
	r, repoID := setupIntakeRulesRouter(t)
	_, code := createIntakeRule(t, r, map[string]any{
		"name":               "partially trusted",
		"match_repo_id":      repoID,
		"apply_target_label": "work",
		"match_author_assoc": []string{"OWNER", "CONTRIBUTOR"},
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 for auto-start with a non-trusted association in the list, got %d", code)
	}
}

func TestIntakeRules_Create_AutoStartWithTrustedAuthor_Returns201(t *testing.T) {
	r, repoID := setupIntakeRulesRouter(t)
	rule, code := createIntakeRule(t, r, map[string]any{
		"name":               "safe auto-start",
		"match_repo_id":      repoID,
		"apply_target_label": "work",
		"match_author_assoc": []string{"OWNER", "MEMBER"},
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", code)
	}
	if rule.ApplyTargetLabel != "work" {
		t.Errorf("apply_target_label = %q, want work", rule.ApplyTargetLabel)
	}
}

func TestIntakeRules_Create_GateLabelNoAuthorConstraintNeeded_Returns201(t *testing.T) {
	r, repoID := setupIntakeRulesRouter(t)
	_, code := createIntakeRule(t, r, map[string]any{
		"name":               "explicit gate",
		"match_repo_id":      repoID,
		"apply_target_label": "not_ready",
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201 (landing on the gate label needs no author constraint), got %d", code)
	}
}

// TestIntakeRules_Create_ScheduleSourceSkipsAutoStartGate_Returns201 verifies
// the auto-start safety gate (which exists to protect against untrusted
// imported issue content, see #331) is not enforced for
// match_source == "schedule": a schedule's target label is already
// human-configured content, and a schedule firing has no author to check,
// so requiring match_author_assoc here would make an intentionally
// supported configuration impossible to save. Mirrors
// internal/schedule.fireIfDue's doc comment and the same case in
// IntakeRulesPage.tsx's autoStartUnsafe.
func TestIntakeRules_Create_ScheduleSourceSkipsAutoStartGate_Returns201(t *testing.T) {
	r, repoID := setupIntakeRulesRouter(t)
	rule, code := createIntakeRule(t, r, map[string]any{
		"name":               "schedule auto-start ok",
		"match_source":       "schedule",
		"match_repo_id":      repoID,
		"apply_target_label": "work", // agent-triggerable, not the gate
		"match_author_assoc": []string{},
	})
	if code != http.StatusCreated {
		t.Fatalf("expected 201 (schedule rules don't need an author-association constraint to target an agent-triggerable label), got %d", code)
	}
	if rule.ApplyTargetLabel != "work" {
		t.Errorf("apply_target_label = %q, want work", rule.ApplyTargetLabel)
	}
}

// TestIntakeRules_Create_ScheduleWithTemplate_Returns400 verifies
// apply_template_id is rejected for match_source == "schedule": the
// scheduler always shapes the created task from the schedule's own
// template (internal/schedule.fireIfDue never reads
// intake.Decision.TemplateID), so this combination would otherwise be a
// silent no-op.
func TestIntakeRules_Create_ScheduleWithTemplate_Returns400(t *testing.T) {
	r, q, repoID := setupIntakeRulesRouterWithQueries(t)
	tmpl, err := q.CreateTaskTemplate(context.Background(), gen.CreateTaskTemplateParams{
		ID:    uuid.NewString(),
		Name:  "triage",
		Title: "Triage",
		Type:  "bug",
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	_, code := createIntakeRule(t, r, map[string]any{
		"name":              "schedule with template",
		"match_source":      "schedule",
		"match_repo_id":     repoID,
		"apply_template_id": tmpl.ID,
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 (apply_template_id has no effect for schedule rules), got %d", code)
	}
}

func TestIntakeRules_Create_UnknownTargetLabel_Returns400(t *testing.T) {
	r, repoID := setupIntakeRulesRouter(t)
	_, code := createIntakeRule(t, r, map[string]any{
		"name":               "bad label",
		"match_repo_id":      repoID,
		"apply_target_label": "does-not-exist",
		"match_author_assoc": []string{"OWNER"},
	})
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
}

func TestIntakeRules_Get_NotFound(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/intake-rules/nope", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestIntakeRules_Update(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	rule, _ := createIntakeRule(t, r, map[string]any{
		"name":         "bug triage",
		"match_source": "issue",
	})

	body := map[string]any{
		"name":         "bug triage v2",
		"match_source": "issue",
		"sort_order":   5,
		"enabled":      false,
	}
	req := httptest.NewRequest(http.MethodPut, "/intake-rules/"+rule.ID, jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var updated apiIntakeRule
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Name != "bug triage v2" {
		t.Errorf("name = %q, want %q", updated.Name, "bug triage v2")
	}
	if updated.Enabled {
		t.Error("expected enabled=false after update")
	}
	if updated.SortOrder != 5 {
		t.Errorf("sort_order = %d, want 5", updated.SortOrder)
	}
}

func TestIntakeRules_Update_NotFound(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	body := map[string]any{"name": "x"}
	req := httptest.NewRequest(http.MethodPut, "/intake-rules/nope", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestIntakeRules_Delete(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	rule, _ := createIntakeRule(t, r, map[string]any{"name": "to delete"})

	req := httptest.NewRequest(http.MethodDelete, "/intake-rules/"+rule.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/intake-rules/"+rule.ID, nil)
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", getW.Code)
	}
}

func TestIntakeRules_Preview_MissingRepoID_Returns400(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/intake-rules/preview", jsonBody(t, map[string]any{
		"rule": map[string]any{"name": "x"},
	}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestIntakeRules_Preview_UnknownRepo_Returns404(t *testing.T) {
	r, _ := setupIntakeRulesRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/intake-rules/preview", jsonBody(t, map[string]any{
		"repo_id": "does-not-exist",
		"rule":    map[string]any{"name": "x"},
	}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// TestIntakeRules_Preview_MatchesImportedTasks exercises the preview
// endpoint's actual shape: it previews against already-imported tasks for
// the repo (see IntakeRulesHandler.Preview's doc comment for why), calling
// the same intake.Match the importer/scheduler use.
func TestIntakeRules_Preview_MatchesImportedTasks(t *testing.T) {
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
		Name:       "preview-repo",
		Path:       t.TempDir(),
		WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := q.CreateSourcedTask(context.Background(), gen.CreateSourcedTaskParams{
		ID:          uuid.NewString(),
		Title:       "Fix crash on startup",
		Description: "boom",
		Type:        "bug",
		Label:       "not_ready",
		RepoID:      repoID,
		WorkflowID:  wfID,
		Attachments: "[]",
		Source:      "github",
		SourceRef:   "acme/widgets#1",
	}); err != nil {
		t.Fatalf("seed imported task: %v", err)
	}

	h := handlers.NewIntakeRulesHandler(q)
	r := chi.NewRouter()
	r.Post("/intake-rules/preview", h.Preview)

	req := httptest.NewRequest(http.MethodPost, "/intake-rules/preview", jsonBody(t, map[string]any{
		"repo_id": repoID,
		"rule": map[string]any{
			"name":                "crash rule",
			"match_title_pattern": "(?i)crash",
		},
	}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Matches []struct {
			TaskID  string `json:"task_id"`
			Matched bool   `json:"matched"`
		} `json:"matches"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 1 || !resp.Matches[0].Matched {
		t.Errorf("expected 1 matched preview result, got %+v", resp.Matches)
	}
}
