package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func setupHealthRouter(t *testing.T, mcpBinary, repoBaseDir, llmBaseURL, llmAPIKey string) http.Handler {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewHealthHandler(q, mcpBinary, repoBaseDir, llmBaseURL, llmAPIKey, "", 24*time.Hour, 7)

	// Register the agents create route too, so tests can seed agent configs
	// and observe how provider-specific checks are (de)emitted.
	agentsH := handlers.NewAgentsHandler(q)
	r := chi.NewRouter()
	r.Post("/agents", agentsH.Create)
	r.Get("/health/providers", h.Providers)
	return r
}

type providersResp struct {
	Checks []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		Detail string `json:"detail"`
		Hint   string `json:"hint"`
	} `json:"checks"`
}

func getProviders(t *testing.T, router http.Handler) providersResp {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/health/providers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp providersResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func (r providersResp) has(id string) bool {
	for _, c := range r.Checks {
		if c.ID == id {
			return true
		}
	}
	return false
}

// TestProvidersEndpoint_NoConfigs verifies the always-present rows are emitted
// and no provider-specific rows appear when there are no agent configs.
func TestProvidersEndpoint_NoConfigs(t *testing.T) {
	router := setupHealthRouter(t, "", "", "", "")
	resp := getProviders(t, router)

	for _, id := range []string{"claude_cli", "mcp_sidecar", "gh_auth", "repo_base_dir", "auto_backup"} {
		if !resp.has(id) {
			t.Errorf("expected always-present check %q", id)
		}
	}
	for _, id := range []string{"qwen_cli", "opencode_cli", "anthropic_api", "llm_api"} {
		if resp.has(id) {
			t.Errorf("did not expect provider check %q with no configs", id)
		}
	}
}

// TestProvidersEndpoint_EmitsChecksForConfiguredProviders verifies a check is
// added once an enabled agent config references that provider.
func TestProvidersEndpoint_EmitsChecksForConfiguredProviders(t *testing.T) {
	router := setupHealthRouter(t, "", "", "", "")

	if w := postJSON(t, router, "/agents", map[string]any{
		"name":     "qwen-agent",
		"provider": "qwen_code",
	}); w.Code != http.StatusCreated {
		t.Fatalf("seed agent: %d %s", w.Code, w.Body.String())
	}

	resp := getProviders(t, router)
	if !resp.has("qwen_cli") {
		t.Errorf("expected qwen_cli check after configuring a qwen_code agent")
	}
	if resp.has("opencode_cli") {
		t.Errorf("did not expect opencode_cli check")
	}
}

// TestProvidersEndpoint_AutoBackupCheck verifies the auto_backup check
// reflects whether BackupDir is configured.
func TestProvidersEndpoint_AutoBackupCheck(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())

	t.Run("disabled when BackupDir is empty", func(t *testing.T) {
		h := handlers.NewHealthHandler(q, "", "", "", "", "", 24*time.Hour, 7)
		r := chi.NewRouter()
		r.Get("/health/providers", h.Providers)
		resp := getProviders(t, r)
		var found bool
		for _, c := range resp.Checks {
			if c.ID == "auto_backup" {
				found = true
				if c.Status != "warn" {
					t.Errorf("expected auto_backup status=warn when BackupDir unset, got %q", c.Status)
				}
			}
		}
		if !found {
			t.Fatalf("expected auto_backup check to be present")
		}
	})

	t.Run("ok when BackupDir is set", func(t *testing.T) {
		h := handlers.NewHealthHandler(q, "", "", "", "", "/data/backups", 24*time.Hour, 7)
		r := chi.NewRouter()
		r.Get("/health/providers", h.Providers)
		resp := getProviders(t, r)
		var found bool
		for _, c := range resp.Checks {
			if c.ID == "auto_backup" {
				found = true
				if c.Status != "ok" {
					t.Errorf("expected auto_backup status=ok when BackupDir set, got %q", c.Status)
				}
			}
		}
		if !found {
			t.Fatalf("expected auto_backup check to be present")
		}
	})
}
