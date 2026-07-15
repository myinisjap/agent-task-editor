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

// setupProvidersRouter returns a chi router wired with the provider-configs
// routes, plus routes to seed an agent config / chat session referencing a
// provider config, for the delete-blocked tests.
func setupProvidersRouter(t *testing.T) (http.Handler, *gen.Queries) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewProviderConfigsHandler(q)
	agentsH := handlers.NewAgentsHandler(q)

	r := chi.NewRouter()
	r.Get("/provider-configs", h.List)
	r.Post("/provider-configs", h.Create)
	r.Get("/provider-configs/{id}", h.Get)
	r.Put("/provider-configs/{id}", h.Update)
	r.Delete("/provider-configs/{id}", h.Delete)
	r.Post("/agents", agentsH.Create)
	return r, q
}

func TestProviderConfigsCreate_RequiresNameAndProvider(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{"provider": "claude"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d: %s", w.Code, w.Body.String())
	}

	w = postJSON(t, router, "/provider-configs", map[string]any{"name": "my-provider"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing provider, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProviderConfigsCreate_RejectsUnknownProvider(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{
		"name": "bogus", "provider": "not-a-real-provider",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProviderConfigsCreate_RoundTrip(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{
		"name":     "my-claude",
		"provider": "claude",
		"model":    "sonnet",
		"env":      `{"ANTHROPIC_API_KEY":"sk-test"}`,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if cfg.Name != "my-claude" || cfg.Provider != "claude" || cfg.Model != "sonnet" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if cfg.Env != `{"ANTHROPIC_API_KEY":"sk-test"}` {
		t.Errorf("expected env to round-trip, got %q", cfg.Env)
	}

	// Defaults env to "{}" when omitted.
	w = postJSON(t, router, "/provider-configs", map[string]any{
		"name": "no-env", "provider": "claude",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg2 gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&cfg2); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if cfg2.Env != "{}" {
		t.Errorf("expected env to default to '{}', got %q", cfg2.Env)
	}
}

func TestProviderConfigsGet_NotFound(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/provider-configs/does-not-exist", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProviderConfigsList(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	postJSON(t, router, "/provider-configs", map[string]any{"name": "a", "provider": "claude"})
	postJSON(t, router, "/provider-configs", map[string]any{"name": "b", "provider": "opencode"})

	req := httptest.NewRequest(http.MethodGet, "/provider-configs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var list []gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 provider configs, got %d", len(list))
	}
}

func TestProviderConfigsUpdate_RoundTrip(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{
		"name": "to-update", "provider": "claude", "model": "sonnet",
	})
	var created gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/provider-configs/"+created.ID, map[string]any{
		"name": "renamed", "provider": "opencode", "model": "gpt-4",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Name != "renamed" || updated.Provider != "opencode" || updated.Model != "gpt-4" {
		t.Errorf("unexpected updated config: %+v", updated)
	}
}

func TestProviderConfigsUpdate_RejectsUnknownProvider(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{"name": "x", "provider": "claude"})
	var created gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/provider-configs/"+created.ID, map[string]any{"provider": "bogus"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProviderConfigsDelete_OK(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{"name": "x", "provider": "claude"})
	var created gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/provider-configs/"+created.ID, nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req)
	if w2.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w2.Code, w2.Body.String())
	}
}

// TestProviderConfigsDelete_BlockedWhenReferenced verifies deleting a
// provider config still referenced by an agent config is blocked with 409,
// since removing it out from under a live agent config would silently break
// dispatch.
func TestProviderConfigsDelete_BlockedWhenReferenced(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{"name": "in-use", "provider": "claude"})
	var pc gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&pc); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = postJSON(t, router, "/agents", map[string]any{
		"name": "agent-using-it", "provider_config_id": pc.ID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("seed agent config: %d %s", w.Code, w.Body.String())
	}

	req := httptest.NewRequest(http.MethodDelete, "/provider-configs/"+pc.ID, nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req)
	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409 when provider config is referenced, got %d: %s", w2.Code, w2.Body.String())
	}
}
