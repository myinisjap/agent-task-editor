package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/repomap"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type noGen struct{}

func (noGen) Generate(context.Context, string, string) (repomap.Completion, error) {
	return repomap.Completion{}, nil
}

func setupRepoMap(t *testing.T) (http.Handler, *gen.Queries) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	svc := repomap.New(db.SQL(), q, func(gen.ProviderConfig) repomap.Generator { return noGen{} })
	h := handlers.NewRepoMapHandler(q, svc)
	r := chi.NewRouter()
	r.Get("/repos/{id}/map", h.Get)
	r.Put("/repos/{id}/map/config", h.UpdateConfig)
	r.Post("/repos/{id}/map/generate", h.Generate)
	r.Get("/repos/{id}/map/runs", h.ListRuns)
	r.Get("/settings/repo-map", h.GetSettings)
	r.Put("/settings/repo-map", h.UpdateSettings)
	if _, err := q.CreateRepo(t.Context(), gen.CreateRepoParams{ID: "r1", Name: "r1", Path: "/nonexistent/r1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateProviderConfig(t.Context(), gen.CreateProviderConfigParams{ID: "pc1", Name: "pc", Provider: "claude", Model: "m", Env: "{}"}); err != nil {
		t.Fatal(err)
	}
	return r, q
}

func TestRepoMapDisabledByDefault(t *testing.T) {
	r, _ := setupRepoMap(t)
	rec := lessonReq(r, http.MethodGet, "/repos/r1/map", nil)
	if rec.Code != 200 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Config struct {
			Enabled bool `json:"enabled"`
		} `json:"config"`
		Pages         []any `json:"pages"`
		CommitsBehind *int  `json:"commits_behind"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Config.Enabled || len(got.Pages) != 0 || got.CommitsBehind != nil {
		t.Fatalf("got %+v", got)
	}
	if rec := lessonReq(r, http.MethodPost, "/repos/r1/map/generate", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("generate while disabled: %d %s", rec.Code, rec.Body)
	}
	if rec := lessonReq(r, http.MethodGet, "/repos/nope/map", nil); rec.Code != 404 {
		t.Fatalf("unknown repo: %d", rec.Code)
	}
}

func TestRepoMapConfigValidation(t *testing.T) {
	r, _ := setupRepoMap(t)
	if rec := lessonReq(r, http.MethodPut, "/repos/r1/map/config", map[string]any{"enabled": true}); rec.Code != 400 {
		t.Fatalf("enable without provider: %d", rec.Code)
	}
	if rec := lessonReq(r, http.MethodPut, "/repos/r1/map/config", map[string]any{"provider_config_id": "missing"}); rec.Code != 400 {
		t.Fatalf("unknown provider: %d", rec.Code)
	}
	if rec := lessonReq(r, http.MethodPut, "/repos/r1/map/config", map[string]any{"min_commits_behind": -1}); rec.Code != 400 {
		t.Fatalf("negative commits: %d", rec.Code)
	}
	rec := lessonReq(r, http.MethodPut, "/repos/r1/map/config", map[string]any{"enabled": true, "provider_config_id": "pc1", "min_commits_behind": 5})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"enabled":true`) || !strings.Contains(rec.Body.String(), `"refresh_interval_hours":168`) {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	// Enabled with a repo path that does not exist: the run is accepted and fails, recorded with its error.
	if rec := lessonReq(r, http.MethodPost, "/repos/r1/map/generate", map[string]any{}); rec.Code != http.StatusAccepted {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body)
	}
}

func TestRepoMapSettings(t *testing.T) {
	r, _ := setupRepoMap(t)
	rec := lessonReq(r, http.MethodGet, "/settings/repo-map", nil)
	if !strings.Contains(rec.Body.String(), `"inject_labels":["plan"]`) {
		t.Fatalf("default: %s", rec.Body)
	}
	rec = lessonReq(r, http.MethodPut, "/settings/repo-map", map[string]any{"inject_labels": []string{"plan", " review-plan "}, "excluded_agent_ids": []string{"a1"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"inject_labels":["plan","review-plan"]`) || !strings.Contains(rec.Body.String(), `"excluded_agent_ids":["a1"]`) {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if rec := lessonReq(r, http.MethodPut, "/settings/repo-map", map[string]any{"inject_labels": []string{"a,b"}}); rec.Code != 400 {
		t.Fatalf("comma label: %d", rec.Code)
	}
}
