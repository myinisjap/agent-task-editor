package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// setupAgentsRouter returns a chi router wired with the agents routes.
func setupAgentsRouter(t *testing.T) http.Handler {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewAgentsHandler(q)

	r := chi.NewRouter()
	r.Post("/agents", h.Create)
	r.Get("/agents", h.List)
	r.Get("/agents/{id}", h.Get)
	r.Put("/agents/{id}", h.Update)
	r.Delete("/agents/{id}", h.Delete)
	return r
}

// TestAgentsCreate_EnabledPluginsAndMCPServers_DefaultOff verifies that
// omitting enabled_plugins/enabled_mcp_servers on create defaults both to
// an empty JSON array (i.e. all plugins/MCP servers off by default).
func TestAgentsCreate_EnabledPluginsAndMCPServers_DefaultOff(t *testing.T) {
	router := setupAgentsRouter(t)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":     "claude-default",
		"provider": "claude",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg gen.AgentConfig
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if cfg.EnabledPlugins != "[]" {
		t.Errorf("expected enabled_plugins to default to '[]', got %q", cfg.EnabledPlugins)
	}
	if cfg.EnabledMcpServers != "[]" {
		t.Errorf("expected enabled_mcp_servers to default to '[]', got %q", cfg.EnabledMcpServers)
	}
}

// TestAgentsCreate_EnabledPluginsAndMCPServers_RoundTrip verifies that
// explicitly selected plugins/MCP servers are persisted and returned as-is.
func TestAgentsCreate_EnabledPluginsAndMCPServers_RoundTrip(t *testing.T) {
	router := setupAgentsRouter(t)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":                "claude-with-selections",
		"provider":            "claude",
		"enabled_plugins":     `["frontend-design@claude-plugins-official"]`,
		"enabled_mcp_servers": `["context7"]`,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg gen.AgentConfig
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var plugins []string
	if err := json.Unmarshal([]byte(cfg.EnabledPlugins), &plugins); err != nil {
		t.Fatalf("unmarshal enabled_plugins: %v", err)
	}
	if len(plugins) != 1 || plugins[0] != "frontend-design@claude-plugins-official" {
		t.Errorf("expected [frontend-design@claude-plugins-official], got %+v", plugins)
	}

	var mcpServers []string
	if err := json.Unmarshal([]byte(cfg.EnabledMcpServers), &mcpServers); err != nil {
		t.Fatalf("unmarshal enabled_mcp_servers: %v", err)
	}
	if len(mcpServers) != 1 || mcpServers[0] != "context7" {
		t.Errorf("expected [context7], got %+v", mcpServers)
	}
}

// TestAgentsUpdate_EnabledPluginsAndMCPServers_RoundTrip verifies that
// updating an existing config's plugin/MCP selections persists correctly.
func TestAgentsUpdate_EnabledPluginsAndMCPServers_RoundTrip(t *testing.T) {
	router := setupAgentsRouter(t)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":     "claude-to-update",
		"provider": "claude",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created gen.AgentConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/agents/"+created.ID, map[string]any{
		"name":                created.Name,
		"provider":            created.Provider,
		"enabled_plugins":     `["oh-my-claudecode@omc","superpowers@claude-plugins-official"]`,
		"enabled_mcp_servers": `["github"]`,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated gen.AgentConfig
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}

	var plugins []string
	if err := json.Unmarshal([]byte(updated.EnabledPlugins), &plugins); err != nil {
		t.Fatalf("unmarshal enabled_plugins: %v", err)
	}
	if len(plugins) != 2 {
		t.Errorf("expected 2 enabled plugins, got %+v", plugins)
	}

	var mcpServers []string
	if err := json.Unmarshal([]byte(updated.EnabledMcpServers), &mcpServers); err != nil {
		t.Fatalf("unmarshal enabled_mcp_servers: %v", err)
	}
	if len(mcpServers) != 1 || mcpServers[0] != "github" {
		t.Errorf("expected [github], got %+v", mcpServers)
	}
}

// TestAgentsCreate_RetryPolicy_Defaults verifies that omitting max_retries/
// retry_backoff_secs on create defaults to 3 retries / 30s base backoff.
func TestAgentsCreate_RetryPolicy_Defaults(t *testing.T) {
	router := setupAgentsRouter(t)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":     "claude-retry-defaults",
		"provider": "claude",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg gen.AgentConfig
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("expected max_retries to default to 3, got %d", cfg.MaxRetries)
	}
	if cfg.RetryBackoffSecs != 30 {
		t.Errorf("expected retry_backoff_secs to default to 30, got %d", cfg.RetryBackoffSecs)
	}
}

// TestAgentsCreate_RetryPolicy_RoundTrip verifies explicit retry policy
// values are persisted and returned as-is.
func TestAgentsCreate_RetryPolicy_RoundTrip(t *testing.T) {
	router := setupAgentsRouter(t)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-retry-custom",
		"provider":           "claude",
		"max_retries":        5,
		"retry_backoff_secs": 60,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg gen.AgentConfig
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("expected max_retries=5, got %d", cfg.MaxRetries)
	}
	if cfg.RetryBackoffSecs != 60 {
		t.Errorf("expected retry_backoff_secs=60, got %d", cfg.RetryBackoffSecs)
	}
}

// TestAgentsUpdate_RetryPolicy_RoundTrip verifies updating retry policy
// fields persists, and omitting them on update preserves existing values.
func TestAgentsUpdate_RetryPolicy_RoundTrip(t *testing.T) {
	router := setupAgentsRouter(t)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":     "claude-retry-update",
		"provider": "claude",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created gen.AgentConfig
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/agents/"+created.ID, map[string]any{
		"name":               created.Name,
		"provider":           created.Provider,
		"max_retries":        0,
		"retry_backoff_secs": 15,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated gen.AgentConfig
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.MaxRetries != 0 {
		t.Errorf("expected max_retries=0 (auto-retry disabled), got %d", updated.MaxRetries)
	}
	if updated.RetryBackoffSecs != 15 {
		t.Errorf("expected retry_backoff_secs=15, got %d", updated.RetryBackoffSecs)
	}

	// Omitting both fields on a subsequent update should preserve them.
	w = putJSON(t, router, "/agents/"+created.ID, map[string]any{
		"name":     created.Name,
		"provider": created.Provider,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var preserved gen.AgentConfig
	if err := json.NewDecoder(w.Body).Decode(&preserved); err != nil {
		t.Fatalf("decode second update response: %v", err)
	}
	if preserved.MaxRetries != 0 {
		t.Errorf("expected max_retries to stay 0, got %d", preserved.MaxRetries)
	}
	if preserved.RetryBackoffSecs != 15 {
		t.Errorf("expected retry_backoff_secs to stay 15, got %d", preserved.RetryBackoffSecs)
	}
}
