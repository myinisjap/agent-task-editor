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

// agentConfigResponse mirrors what the API actually sends: gen.AgentConfig's
// 0/1 flag columns serialize as real JSON booleans (see agentConfigView in
// agents.go), not the raw int64s on the sqlc-generated struct.
type agentConfigResponse struct {
	gen.AgentConfig
	Enabled         bool `json:"enabled"`
	ResumeSessions  bool `json:"resume_sessions"`
	SubtasksEnabled bool `json:"subtasks_enabled"`
}

// setupAgentsRouter returns a chi router wired with the agents routes, plus
// the underlying queries handle so tests can seed a provider config to
// reference (agent configs now require an existing provider_config_id).
func setupAgentsRouter(t *testing.T) (http.Handler, *gen.Queries) {
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
	r.Get("/agent-models", h.GetModels)
	return r, q
}

// mkProviderConfig seeds a claude provider config and returns its id, for use
// as an agent config's provider_config_id in tests.
func mkProviderConfig(t *testing.T, q *gen.Queries) string {
	t.Helper()
	pc, err := q.CreateProviderConfig(context.Background(), gen.CreateProviderConfigParams{
		ID:       uuid.NewString(),
		Name:     "test-provider",
		Provider: "claude",
		Model:    "sonnet",
		Env:      "{}",
	})
	if err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	return pc.ID
}

// TestAgentsList_Pagination verifies limit/after cursor pagination walks the
// full agent-config list (enabled or not) without dupes or gaps.
func TestAgentsList_Pagination(t *testing.T) {
	r, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	for i := 0; i < 5; i++ {
		w := postJSON(t, r, "/agents", map[string]any{
			"name": "agent", "provider_config_id": pcID,
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("create agent config: %d %s", w.Code, w.Body.String())
		}
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		qs := "?limit=2"
		if cursor != "" {
			qs += "&after=" + cursor
		}
		req := httptest.NewRequest(http.MethodGet, "/agents"+qs, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET agents%s: expected 200, got %d: %s", qs, w.Code, w.Body)
		}
		var page []agentConfigResponse
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

// TestAgentsCreate_EnabledPluginsAndMCPServers_DefaultOff verifies that
// omitting enabled_plugins/enabled_mcp_servers on create defaults both to
// an empty JSON array (i.e. all plugins/MCP servers off by default).
func TestAgentsCreate_EnabledPluginsAndMCPServers_DefaultOff(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-default",
		"provider_config_id": pcID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg agentConfigResponse
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
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":                "claude-with-selections",
		"provider_config_id":  pcID,
		"enabled_plugins":     `["frontend-design@claude-plugins-official"]`,
		"enabled_mcp_servers": `["context7"]`,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg agentConfigResponse
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
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-to-update",
		"provider_config_id": pcID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/agents/"+created.ID, map[string]any{
		"name":                created.Name,
		"provider_config_id":  created.ProviderConfigID,
		"enabled_plugins":     `["oh-my-claudecode@omc","superpowers@claude-plugins-official"]`,
		"enabled_mcp_servers": `["github"]`,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated agentConfigResponse
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

// TestAgentsCreate_CommandFilters_DefaultOff verifies that omitting
// command_allowlist/command_denylist on create defaults both to an empty
// JSON array (i.e. no restriction by default).
func TestAgentsCreate_CommandFilters_DefaultOff(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-cmd-default",
		"provider_config_id": pcID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if cfg.CommandAllowlist != "[]" {
		t.Errorf("expected command_allowlist to default to '[]', got %q", cfg.CommandAllowlist)
	}
	if cfg.CommandDenylist != "[]" {
		t.Errorf("expected command_denylist to default to '[]', got %q", cfg.CommandDenylist)
	}
}

// TestAgentsCreate_CommandFilters_RoundTrip verifies that explicitly set
// command allow/deny lists are persisted and returned as-is.
func TestAgentsCreate_CommandFilters_RoundTrip(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-with-cmd-filters",
		"provider_config_id": pcID,
		"command_allowlist":  `["git *", "npm test"]`,
		"command_denylist":   `["rm -rf *"]`,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	var allow []string
	if err := json.Unmarshal([]byte(cfg.CommandAllowlist), &allow); err != nil {
		t.Fatalf("unmarshal command_allowlist: %v", err)
	}
	if len(allow) != 2 || allow[0] != "git *" || allow[1] != "npm test" {
		t.Errorf("expected [git *, npm test], got %+v", allow)
	}

	var deny []string
	if err := json.Unmarshal([]byte(cfg.CommandDenylist), &deny); err != nil {
		t.Fatalf("unmarshal command_denylist: %v", err)
	}
	if len(deny) != 1 || deny[0] != "rm -rf *" {
		t.Errorf("expected [rm -rf *], got %+v", deny)
	}
}

// TestAgentsUpdate_CommandFilters_RoundTrip verifies that updating an
// existing config's command allow/deny lists persists correctly.
func TestAgentsUpdate_CommandFilters_RoundTrip(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-cmd-to-update",
		"provider_config_id": pcID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/agents/"+created.ID, map[string]any{
		"name":               created.Name,
		"provider_config_id": created.ProviderConfigID,
		"command_allowlist":  `["go *"]`,
		"command_denylist":   `["curl *", "sudo *"]`,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}

	var allow []string
	if err := json.Unmarshal([]byte(updated.CommandAllowlist), &allow); err != nil {
		t.Fatalf("unmarshal command_allowlist: %v", err)
	}
	if len(allow) != 1 || allow[0] != "go *" {
		t.Errorf("expected [go *], got %+v", allow)
	}

	var deny []string
	if err := json.Unmarshal([]byte(updated.CommandDenylist), &deny); err != nil {
		t.Fatalf("unmarshal command_denylist: %v", err)
	}
	if len(deny) != 2 || deny[0] != "curl *" || deny[1] != "sudo *" {
		t.Errorf("expected [curl *, sudo *], got %+v", deny)
	}
}

// TestAgentsCreate_RetryPolicy_Defaults verifies that omitting max_retries/
// retry_backoff_secs on create defaults to 3 retries / 30s base backoff.
func TestAgentsCreate_RetryPolicy_Defaults(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-retry-defaults",
		"provider_config_id": pcID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg agentConfigResponse
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
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-retry-custom",
		"provider_config_id": pcID,
		"max_retries":        5,
		"retry_backoff_secs": 60,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var cfg agentConfigResponse
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

// TestAgentsCreate_RetryPolicy_RejectsNegative verifies the API rejects
// negative max_retries/retry_backoff_secs on create, since the frontend's
// min-bound enforcement can be bypassed by a direct API client.
func TestAgentsCreate_RetryPolicy_RejectsNegative(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-negative-max-retries",
		"provider_config_id": pcID,
		"max_retries":        -1,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative max_retries, got %d: %s", w.Code, w.Body.String())
	}

	w = postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-negative-backoff",
		"provider_config_id": pcID,
		"retry_backoff_secs": -5,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative retry_backoff_secs, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAgentsUpdate_RetryPolicy_RejectsNegative verifies the API rejects
// negative max_retries/retry_backoff_secs on update.
func TestAgentsUpdate_RetryPolicy_RejectsNegative(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-retry-update-negative",
		"provider_config_id": pcID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/agents/"+created.ID, map[string]any{
		"name":               created.Name,
		"provider_config_id": created.ProviderConfigID,
		"max_retries":        -1,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative max_retries, got %d: %s", w.Code, w.Body.String())
	}

	w = putJSON(t, router, "/agents/"+created.ID, map[string]any{
		"name":               created.Name,
		"provider_config_id": created.ProviderConfigID,
		"retry_backoff_secs": -5,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for negative retry_backoff_secs, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAgentsUpdate_RetryPolicy_RoundTrip verifies updating retry policy
// fields persists, and omitting them on update preserves existing values.
func TestAgentsUpdate_RetryPolicy_RoundTrip(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name":               "claude-retry-update",
		"provider_config_id": pcID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	w = putJSON(t, router, "/agents/"+created.ID, map[string]any{
		"name":               created.Name,
		"provider_config_id": created.ProviderConfigID,
		"max_retries":        0,
		"retry_backoff_secs": 15,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated agentConfigResponse
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
		"name":               created.Name,
		"provider_config_id": created.ProviderConfigID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var preserved agentConfigResponse
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

// ---------- Get / Delete / labelConflict ----------

func TestAgentsGet_OK(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{"name": "cfg-a", "provider_config_id": pcID})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/agents/"+created.ID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got.ID != created.ID || got.Name != "cfg-a" {
		t.Errorf("expected config cfg-a with id %s, got %+v", created.ID, got)
	}
}

func TestAgentsGet_Unknown(t *testing.T) {
	router, _ := setupAgentsRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/agents/"+uuid.NewString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAgentsDelete_OK(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{"name": "cfg-a", "provider_config_id": pcID})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/agents/"+created.ID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/agents/"+created.ID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected deleted config to 404 on Get, got %d", w.Code)
	}
}

func TestAgentsDelete_Unknown(t *testing.T) {
	router, _ := setupAgentsRouter(t)

	// Deleting an unknown id is a no-op DELETE (0 rows affected), not an error.
	req := httptest.NewRequest(http.MethodDelete, "/agents/"+uuid.NewString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAgentsCreate_LabelConflict_DisablesNewConfig exercises labelConflict's
// conflict branch: creating a second enabled config that shares a label with
// an already-enabled config should succeed but come back disabled, with the
// conflicting config's name surfaced in X-Label-Conflict.
func TestAgentsCreate_LabelConflict_DisablesNewConfig(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name": "first", "provider_config_id": pcID, "labels": `["work"]`,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create first: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var first agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&first); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if !first.Enabled {
		t.Fatalf("expected first config to be enabled, got %+v", first)
	}

	w = postJSON(t, router, "/agents", map[string]any{
		"name": "second", "provider_config_id": pcID, "labels": `["work"]`,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create second: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if conflict := w.Header().Get("X-Label-Conflict"); conflict != "first" {
		t.Errorf("expected X-Label-Conflict 'first', got %q", conflict)
	}
	var second agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&second); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if second.Enabled {
		t.Errorf("expected second config to start disabled due to label conflict, got enabled")
	}
}

// TestAgentsCreate_NoLabelConflict_WhenLabelsDiffer verifies the non-conflict
// path of labelConflict: distinct labels never trigger a disable.
func TestAgentsCreate_NoLabelConflict_WhenLabelsDiffer(t *testing.T) {
	router, q := setupAgentsRouter(t)
	pcID := mkProviderConfig(t, q)

	w := postJSON(t, router, "/agents", map[string]any{
		"name": "first", "provider_config_id": pcID, "labels": `["work"]`,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create first: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	w = postJSON(t, router, "/agents", map[string]any{
		"name": "second", "provider_config_id": pcID, "labels": `["testing"]`,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create second: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if conflict := w.Header().Get("X-Label-Conflict"); conflict != "" {
		t.Errorf("expected no X-Label-Conflict header, got %q", conflict)
	}
	var second agentConfigResponse
	if err := json.NewDecoder(w.Body).Decode(&second); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if !second.Enabled {
		t.Errorf("expected second config to be enabled (no conflict), got disabled")
	}
}

// TestAgentsGetModels_MissingProvider verifies the required-query-param
// validation (400) when ?provider is omitted.
func TestAgentsGetModels_MissingProvider(t *testing.T) {
	router, _ := setupAgentsRouter(t)
	w := httptest.NewRequest(http.MethodGet, "/agent-models", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, w)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAgentsGetModels_KnownFixedListProvider verifies a provider with a
// hardcoded model list (claude) returns that list and a sensible default.
func TestAgentsGetModels_KnownFixedListProvider(t *testing.T) {
	router, _ := setupAgentsRouter(t)
	w := httptest.NewRequest(http.MethodGet, "/agent-models?provider=claude", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, w)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Provider     string   `json:"provider"`
		DefaultModel string   `json:"default_model"`
		Models       []string `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Provider != "claude" {
		t.Errorf("provider = %q, want claude", resp.Provider)
	}
	if resp.DefaultModel != "sonnet" {
		t.Errorf("default_model = %q, want sonnet", resp.DefaultModel)
	}
	if len(resp.Models) == 0 {
		t.Errorf("expected non-empty models list")
	}
}

// TestAgentsGetModels_KnownProviderNoFixedList verifies a known provider
// without a hardcoded model list (e.g. qwen_code) returns 200 with an empty
// models array rather than a 404, so the UI can fall back to free-text entry.
func TestAgentsGetModels_KnownProviderNoFixedList(t *testing.T) {
	router, _ := setupAgentsRouter(t)
	w := httptest.NewRequest(http.MethodGet, "/agent-models?provider=qwen_code", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, w)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Models []string `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Models) != 0 {
		t.Errorf("expected empty models list, got %v", resp.Models)
	}
}

// TestAgentsGetModels_UnknownProvider verifies an unrecognized provider
// string returns 404.
func TestAgentsGetModels_UnknownProvider(t *testing.T) {
	router, _ := setupAgentsRouter(t)
	w := httptest.NewRequest(http.MethodGet, "/agent-models?provider=nonexistent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, w)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
