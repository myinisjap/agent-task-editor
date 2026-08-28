package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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

// TestProviderConfigsCreate_RejectsDeprecatedProvider verifies anthropic and
// llm are rejected for new provider configs with a message distinguishing
// "deprecated" from "unknown" (they remain in knownProviders so existing
// configs keep working, but new writes are blocked).
func TestProviderConfigsCreate_RejectsDeprecatedProvider(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	for _, p := range []string{"anthropic", "llm"} {
		w := postJSON(t, router, "/provider-configs", map[string]any{
			"name": "deprecated-" + p, "provider": p,
		})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("provider %q: expected 400, got %d: %s", p, w.Code, w.Body.String())
		}
		body := w.Body.String()
		if !strings.Contains(body, "deprecated") || !strings.Contains(body, p) {
			t.Errorf("provider %q: expected deprecation message mentioning provider, got %q", p, body)
		}
	}
}

// TestProviderConfigsCreate_RejectsOpenAIAlias verifies "openai" — the dead
// dropdown option that aliased to the deprecated llm/OpenAI-compatible path
// — is rejected too. It was never in knownProviders, so it surfaces as an
// "unknown provider" 400 rather than the "deprecated" message; either way,
// the fix here is that it can no longer be selected in the UI (see
// agentTemplates.ts) so this 400 should never be user-visible in practice.
func TestProviderConfigsCreate_RejectsOpenAIAlias(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{
		"name": "openai-alias", "provider": "openai",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for openai, got %d: %s", w.Code, w.Body.String())
	}
}

// TestProviderConfigsCreate_RoundTrip verifies name/provider/model round-trip
// on create. env is intentionally NOT expected to round-trip verbatim: it's
// write-only (see provider_env.go), so the response masks the value while
// keeping the key name — see TestProviderConfigsCreate_RedactsEnvValues and
// TestProviderConfigsGet_RedactsEnvValues for the redaction behavior itself.
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
	if cfg.Env != `{"ANTHROPIC_API_KEY":"***"}` {
		t.Errorf("expected env value to be redacted in the response, got %q", cfg.Env)
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

// TestProviderConfigsGet_RedactsEnvValues verifies GET /provider-configs/{id}
// never returns the secret value, even as a raw string in the response body,
// while the key name remains visible for the UI.
func TestProviderConfigsGet_RedactsEnvValues(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{
		"name":     "secret-holder",
		"provider": "claude",
		"env":      `{"ANTHROPIC_API_KEY":"sk-test"}`,
	})
	var created gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/provider-configs/"+created.ID, nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	body := w2.Body.String()
	if strings.Contains(body, "sk-test") {
		t.Errorf("expected secret value to be redacted from GET response, got body %q", body)
	}
	if !strings.Contains(body, "ANTHROPIC_API_KEY") {
		t.Errorf("expected env key name to remain visible, got body %q", body)
	}
}

// TestProviderConfigsList_RedactsEnvValues is the list-endpoint analog of
// TestProviderConfigsGet_RedactsEnvValues.
func TestProviderConfigsList_RedactsEnvValues(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	postJSON(t, router, "/provider-configs", map[string]any{
		"name":     "secret-holder",
		"provider": "claude",
		"env":      `{"ANTHROPIC_API_KEY":"sk-test"}`,
	})

	req := httptest.NewRequest(http.MethodGet, "/provider-configs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "sk-test") {
		t.Errorf("expected secret value to be redacted from list response, got body %q", body)
	}
	if !strings.Contains(body, "ANTHROPIC_API_KEY") {
		t.Errorf("expected env key name to remain visible, got body %q", body)
	}
}

// TestProviderConfigsUpdate_PreservesRedactedSecret verifies that PUTting the
// sentinel ("***") for a key — e.g. what a client gets back from re-reading
// its own previous write, unmodified — preserves the real stored value
// rather than persisting the literal sentinel. Checked directly against the
// DB, since the HTTP response is (correctly) always redacted.
func TestProviderConfigsUpdate_PreservesRedactedSecret(t *testing.T) {
	router, q := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{
		"name":     "keep-secret",
		"provider": "claude",
		"env":      `{"ANTHROPIC_API_KEY":"sk-test"}`,
	})
	var created gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/provider-configs/"+created.ID, map[string]any{
		"name": "keep-secret-renamed",
		"env":  `{"ANTHROPIC_API_KEY":"***"}`,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	stored, err := q.GetProviderConfig(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get provider config: %v", err)
	}
	if stored.Env != `{"ANTHROPIC_API_KEY":"sk-test"}` {
		t.Errorf("expected stored secret to be preserved, got %q", stored.Env)
	}
}

// TestProviderConfigsUpdate_ReplacesEnvValue verifies a genuinely new value
// for an existing key overwrites the stored value.
func TestProviderConfigsUpdate_ReplacesEnvValue(t *testing.T) {
	router, q := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{
		"name":     "replace-secret",
		"provider": "claude",
		"env":      `{"ANTHROPIC_API_KEY":"sk-test"}`,
	})
	var created gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/provider-configs/"+created.ID, map[string]any{
		"env": `{"ANTHROPIC_API_KEY":"sk-new"}`,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	stored, err := q.GetProviderConfig(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get provider config: %v", err)
	}
	if stored.Env != `{"ANTHROPIC_API_KEY":"sk-new"}` {
		t.Errorf("expected stored secret to be replaced, got %q", stored.Env)
	}
}

// TestProviderConfigsUpdate_RemovesOmittedKey verifies that a key present in
// the stored env but omitted from a non-empty incoming env is deleted (the
// "clearing a key removes it" contract), while an entirely omitted env
// (empty string) still means "unchanged".
func TestProviderConfigsUpdate_RemovesOmittedKey(t *testing.T) {
	router, q := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{
		"name":     "two-keys",
		"provider": "claude",
		"env":      `{"ANTHROPIC_API_KEY":"sk-test","OTHER_KEY":"other-val"}`,
	})
	var created gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	// PUT with only one of the two keys: the omitted key is deleted.
	w = putJSON(t, router, "/provider-configs/"+created.ID, map[string]any{
		"env": `{"ANTHROPIC_API_KEY":"***"}`,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	stored, err := q.GetProviderConfig(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get provider config: %v", err)
	}
	if stored.Env != `{"ANTHROPIC_API_KEY":"sk-test"}` {
		t.Errorf("expected OTHER_KEY to be removed and ANTHROPIC_API_KEY preserved, got %q", stored.Env)
	}

	// A subsequent PUT that omits env entirely (empty string) must leave it
	// unchanged, not clear it.
	w = putJSON(t, router, "/provider-configs/"+created.ID, map[string]any{
		"name": "two-keys-renamed",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	stored, err = q.GetProviderConfig(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get provider config: %v", err)
	}
	if stored.Env != `{"ANTHROPIC_API_KEY":"sk-test"}` {
		t.Errorf("expected env to remain unchanged when omitted from PUT body, got %q", stored.Env)
	}
}

// TestProviderConfigsUpdate_RejectsMalformedEnv verifies a non-object env
// value 400s rather than being silently accepted or wiping the stored env.
func TestProviderConfigsUpdate_RejectsMalformedEnv(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{"name": "x", "provider": "claude"})
	var created gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/provider-configs/"+created.ID, map[string]any{"env": "not json"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed env, got %d: %s", w.Code, w.Body.String())
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

// TestProviderConfigsList_Pagination verifies limit/after cursor pagination
// walks the full list without dupes or gaps, and that a full-size page
// advertises no further cursor.
func TestProviderConfigsList_Pagination(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	for i := 0; i < 5; i++ {
		w := postJSON(t, router, "/provider-configs", map[string]any{
			"name": "cfg", "provider": "claude",
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("create provider config: %d %s", w.Code, w.Body.String())
		}
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		q := "?limit=2"
		if cursor != "" {
			q += "&after=" + cursor
		}
		req := httptest.NewRequest(http.MethodGet, "/provider-configs"+q, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET provider-configs%s: expected 200, got %d: %s", q, w.Code, w.Body)
		}
		var page []gen.ProviderConfig
		if err := json.NewDecoder(w.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page) > 2 {
			t.Fatalf("page returned %d configs, expected <= 2", len(page))
		}
		for _, c := range page {
			if seen[c.ID] {
				t.Fatalf("config %s returned on more than one page", c.ID)
			}
			seen[c.ID] = true
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
		t.Fatalf("expected to page through all 5 configs, saw %d", len(seen))
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

// TestProviderConfigsUpdate_OmittedModelPreserved verifies that omitting
// "model" from a PUT request preserves the existing model rather than
// blanking it (blanking it would break dispatch for agent configs
// referencing this provider config).
func TestProviderConfigsUpdate_OmittedModelPreserved(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{
		"name": "keep-model", "provider": "claude", "model": "sonnet",
	})
	var created gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/provider-configs/"+created.ID, map[string]any{
		"name": "renamed-only",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Model != "sonnet" {
		t.Errorf("expected model to be preserved as 'sonnet', got %q", updated.Model)
	}
	if updated.Name != "renamed-only" {
		t.Errorf("expected name to be updated to 'renamed-only', got %q", updated.Name)
	}
}

// TestProviderConfigsUpdate_RejectsChangingToDeprecatedProvider verifies a
// config on a non-deprecated provider cannot be switched to anthropic/llm.
func TestProviderConfigsUpdate_RejectsChangingToDeprecatedProvider(t *testing.T) {
	router, _ := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{"name": "x", "provider": "claude"})
	var created gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/provider-configs/"+created.ID, map[string]any{"provider": "anthropic"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 changing to deprecated provider, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "deprecated") {
		t.Errorf("expected deprecation message, got %q", w.Body.String())
	}
}

// TestProviderConfigsUpdate_ExistingDeprecatedProviderKeepsWorking verifies
// that a config already on a deprecated provider (e.g. seeded before the
// deprecation, or migrated data) can still be edited as long as the
// provider itself isn't changing — the whole point of deprecating rather
// than deleting these providers is that existing configs keep working.
func TestProviderConfigsUpdate_ExistingDeprecatedProviderKeepsWorking(t *testing.T) {
	router, q := setupProvidersRouter(t)

	seeded, err := q.CreateProviderConfig(context.Background(), gen.CreateProviderConfigParams{
		ID:       "seed-anthropic",
		Name:     "legacy-anthropic",
		Provider: "anthropic",
		Model:    "claude-3-opus",
		Env:      "{}",
	})
	if err != nil {
		t.Fatalf("seed provider config: %v", err)
	}

	// Sending the same (unchanged) provider back must succeed.
	w := putJSON(t, router, "/provider-configs/"+seeded.ID, map[string]any{
		"name": "legacy-anthropic-renamed", "provider": "anthropic",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 updating unrelated fields on existing deprecated config, got %d: %s", w.Code, w.Body.String())
	}

	// Omitting provider entirely (meaning "unchanged") must also succeed.
	w = putJSON(t, router, "/provider-configs/"+seeded.ID, map[string]any{
		"model": "claude-3-sonnet",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 updating model on existing deprecated config with provider omitted, got %d: %s", w.Code, w.Body.String())
	}
	var updated gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Provider != "anthropic" {
		t.Errorf("expected provider to remain 'anthropic', got %q", updated.Provider)
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

// TestProviderConfigsDelete_BlockedWhenReferencedByChatSession covers the
// second (chat-session) branch of the referenced-config guard, distinct from
// the agent-config branch above.
func TestProviderConfigsDelete_BlockedWhenReferencedByChatSession(t *testing.T) {
	router, q := setupProvidersRouter(t)

	w := postJSON(t, router, "/provider-configs", map[string]any{"name": "in-use-by-chat", "provider": "claude"})
	var pc gen.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&pc); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:   repoID,
		Name: "chat-repo",
		Path: t.TempDir(),
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := q.CreateChatSession(context.Background(), gen.CreateChatSessionParams{
		ID:               uuid.NewString(),
		RepoID:           repoID,
		ProviderConfigID: pc.ID,
		Title:            "chat using it",
	}); err != nil {
		t.Fatalf("seed chat session: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/provider-configs/"+pc.ID, nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req)
	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409 when provider config is referenced by a chat session, got %d: %s", w2.Code, w2.Body.String())
	}
}
