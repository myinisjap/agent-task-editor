package handlers_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/intakehook"
	"github.com/myinisjap/agent-task-editor/backend/internal/recipes"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type recipeEnv struct {
	r      http.Handler
	q      *gen.Queries
	h      *handlers.RecipesHandler
	repoID string
	gate   string
	work   string
}

func setupRecipes(t *testing.T) *recipeEnv {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()
	wfs, err := q.ListWorkflows(ctx)
	if err != nil || len(wfs) == 0 {
		t.Fatalf("seeded workflow: %v", err)
	}
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfs[0].ID}); err != nil {
		t.Fatal(err)
	}
	labels, _ := q.ListWorkflowLabels(ctx, wfs[0].ID)
	e := &recipeEnv{q: q, repoID: repoID}
	for _, l := range labels {
		if l.AgentIgnore != 0 && e.gate == "" {
			e.gate = l.Name
		}
		if l.AgentIgnore == 0 && l.IsTerminal == 0 && e.work == "" {
			e.work = l.Name
		}
	}
	if e.gate == "" || e.work == "" {
		t.Fatalf("labels: %+v", labels)
	}
	e.h = handlers.NewRecipesHandler(db.SQL(), q)
	ih := handlers.NewIntakeEndpointsHandler(q, intakehook.NewService(db.SQL(), q, nopPub{}))
	r := chi.NewRouter()
	r.Get("/automation-recipes", e.h.List)
	r.Post("/automation-recipes/{id}/install", e.h.Install)
	r.Delete("/automation-recipes/{id}", e.h.Remove)
	r.Post("/intake/{endpoint_id}", ih.Receive)
	e.r = r
	return e
}

func (e *recipeEnv) do(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func (e *recipeEnv) install(id string, params map[string]string, confirm bool) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]any{"params": params, "confirm_auto_start": confirm})
	return e.do("POST", "/automation-recipes/"+id+"/install", string(b), nil)
}

func (e *recipeEnv) counts(t *testing.T) (tpl, sch, ep, rule int) {
	t.Helper()
	ctx := context.Background()
	a, _ := e.q.ListTaskTemplates(ctx)
	b, _ := e.q.ListTaskSchedules(ctx)
	c, _ := e.q.ListIntakeEndpoints(ctx)
	d, _ := e.q.ListIntakeRules(ctx)
	return len(a), len(b), len(c), len(d)
}

func TestRecipes_EachExpandsToValidObjects(t *testing.T) {
	for _, rc := range recipes.All() {
		t.Run(rc.ID, func(t *testing.T) {
			e := setupRecipes(t)
			w := e.install(rc.ID, map[string]string{"repo_id": e.repoID}, false)
			if w.Code != http.StatusCreated {
				t.Fatalf("install: %d %s", w.Code, w.Body)
			}
			var res struct {
				Template gen.TaskTemplate  `json:"template"`
				Schedule *gen.TaskSchedule `json:"schedule"`
				Endpoint *struct {
					ID, RecipeID, TargetLabel string
				} `json:"endpoint"`
				Secret  string `json:"secret"`
				URL     string `json:"url"`
				Snippet string `json:"snippet"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
				t.Fatal(err)
			}
			if res.Template.RecipeID != rc.ID || res.Template.Name == "" {
				t.Fatalf("template: %+v", res.Template)
			}
			tpl, sch, ep, rule := e.counts(t)
			switch rc.Kind {
			case "schedule":
				if tpl != 1 || sch != 1 || ep != 0 || rule != 0 || res.Schedule == nil {
					t.Fatalf("counts %d %d %d %d", tpl, sch, ep, rule)
				}
				// parked by default
				if res.Schedule.TargetLabel != e.gate || res.Schedule.RecipeID != rc.ID {
					t.Fatalf("schedule: %+v", res.Schedule)
				}
			case "webhook":
				if tpl != 1 || sch != 0 || ep != 1 || rule != 1 {
					t.Fatalf("counts %d %d %d %d", tpl, sch, ep, rule)
				}
				if !strings.HasPrefix(res.Secret, "whsec_") || !strings.Contains(res.URL, "/api/v1/intake/") ||
					!strings.Contains(res.Snippet, res.URL) {
					t.Fatalf("webhook output: %+v", res)
				}
				if strings.Contains(res.Snippet, res.Secret) {
					t.Fatal("snippet must not embed the secret")
				}
				eps, _ := e.q.ListIntakeEndpoints(context.Background())
				if eps[0].TargetLabel != "" || eps[0].RecipeID != rc.ID {
					t.Fatalf("endpoint not parked/tagged: %+v", eps[0])
				}
			}
			// listing reflects installed state
			lw := e.do("GET", "/automation-recipes", "", nil)
			var list []struct {
				ID        string `json:"id"`
				Installed bool   `json:"installed"`
			}
			_ = json.Unmarshal(lw.Body.Bytes(), &list)
			if len(list) != len(recipes.All()) {
				t.Fatalf("list: %s", lw.Body)
			}
			for _, it := range list {
				if it.Installed != (it.ID == rc.ID) {
					t.Fatalf("installed flag wrong for %s", it.ID)
				}
			}
			// second install conflicts
			if w := e.install(rc.ID, map[string]string{"repo_id": e.repoID}, false); w.Code != http.StatusConflict {
				t.Fatalf("reinstall: %d", w.Code)
			}
		})
	}
}

func TestRecipes_CIWebhookParkedAndDedupes(t *testing.T) {
	e := setupRecipes(t)
	w := e.install("ci-red-main", map[string]string{"repo_id": e.repoID}, false)
	var res struct {
		Secret   string `json:"secret"`
		Endpoint struct {
			URLPath string `json:"url_path"`
		} `json:"endpoint"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	body := `{"repository":"o/r","branch":"main","workflow":"CI","sha":"abc","actor":"x","run_url":"http://run/1"}`
	mac := hmac.New(sha256.New, []byte(res.Secret))
	mac.Write([]byte(body))
	hdr := map[string]string{"X-ATE-Signature-256": "sha256=" + hex.EncodeToString(mac.Sum(nil))}
	w1 := e.do("POST", res.Endpoint.URLPath[len("/api/v1"):], body, hdr)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", w1.Code, w1.Body)
	}
	var created struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &created)
	task, err := e.q.GetTask(context.Background(), created.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Label != e.gate {
		t.Fatalf("label %q, want parked %q", task.Label, e.gate)
	}
	if !strings.Contains(task.Title, "CI red on o/r@main") || !strings.Contains(task.Description, "Fix failing CI") && !strings.Contains(task.Description, "CI pipeline") {
		t.Fatalf("task: %q / %q", task.Title, task.Description)
	}
	if task.MatchedRuleID == nil {
		t.Fatal("rule not matched")
	}
	w2 := e.do("POST", res.Endpoint.URLPath[len("/api/v1"):], body, hdr)
	if w2.Code != http.StatusOK || !strings.Contains(w2.Body.String(), `"deduped":true`) {
		t.Fatalf("second: %d %s", w2.Code, w2.Body)
	}
}

func TestRecipes_AutoStartNeedsConfirmation(t *testing.T) {
	e := setupRecipes(t)
	params := map[string]string{"repo_id": e.repoID, "target_label": e.work}
	w := e.install("docs-drift-check", params, false)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "auto_start_confirmation_required") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if a, b, c, d := e.counts(t); a+b+c+d != 0 {
		t.Fatal("nothing should be created without confirmation")
	}
	if w := e.install("docs-drift-check", params, true); w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), "agent-triggerable") {
		t.Fatalf("confirmed: %d %s", w.Code, w.Body)
	}
}

func TestRecipes_Validation(t *testing.T) {
	e := setupRecipes(t)
	for name, params := range map[string]map[string]string{
		"no repo":   {},
		"bad repo":  {"repo_id": "nope"},
		"bad cron":  {"repo_id": e.repoID, "cron_expr": "nonsense"},
		"bad label": {"repo_id": e.repoID, "target_label": "zzz"},
	} {
		if w := e.install("weekly-dependency-updates", params, true); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := e.install("ci-red-main", map[string]string{"repo_id": e.repoID, "branch": "a b;"}, false); w.Code != http.StatusBadRequest {
		t.Errorf("branch: %d", w.Code)
	}
	if w := e.install("nope", nil, false); w.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", w.Code)
	}
}

func TestRecipes_InstallIsTransactional(t *testing.T) {
	e := setupRecipes(t)
	for _, failAt := range []string{"template", "endpoint", "rule"} {
		e.h.SetAfterStep(func(s string) error {
			if s == failAt {
				return errors.New("boom")
			}
			return nil
		})
		if w := e.install("ci-red-main", map[string]string{"repo_id": e.repoID}, false); w.Code != http.StatusInternalServerError {
			t.Fatalf("%s: %d", failAt, w.Code)
		}
		if a, b, c, d := e.counts(t); a+b+c+d != 0 {
			t.Fatalf("fail at %s left objects: %d %d %d %d", failAt, a, b, c, d)
		}
	}
}

func TestRecipes_RemoveDeletesOnlyTagged(t *testing.T) {
	e := setupRecipes(t)
	ctx := context.Background()
	// hand-made objects that must survive
	tpl, err := e.q.CreateTaskTemplate(ctx, gen.CreateTaskTemplateParams{ID: uuid.NewString(), Name: "mine", Type: "chore"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.CreateTaskSchedule(ctx, gen.CreateTaskScheduleParams{ID: uuid.NewString(), TemplateID: tpl.ID, RepoID: e.repoID, CronExpr: "0 9 * * 1", TargetLabel: e.gate, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.CreateIntakeEndpoint(ctx, gen.CreateIntakeEndpointParams{ID: uuid.NewString(), Name: "mine", RepoID: e.repoID, Secret: "s", Enabled: true, TitleTemplate: "t"}); err != nil {
		t.Fatal(err)
	}
	e.install("ci-red-main", map[string]string{"repo_id": e.repoID}, false)
	e.install("weekly-dependency-updates", map[string]string{"repo_id": e.repoID}, false)
	if a, b, c, d := e.counts(t); a != 3 || b != 2 || c != 2 || d != 1 {
		t.Fatalf("pre counts %d %d %d %d", a, b, c, d)
	}
	if w := e.do("DELETE", "/automation-recipes/ci-red-main", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	if a, b, c, d := e.counts(t); a != 2 || b != 2 || c != 1 || d != 0 {
		t.Fatalf("post counts %d %d %d %d", a, b, c, d)
	}
	if w := e.do("DELETE", "/automation-recipes/ci-red-main", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("second remove: %d", w.Code)
	}
}
