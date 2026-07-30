package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
)

// TestGitHubAuthStatus_ReturnsWellFormedResponse exercises the handler
// end-to-end. It doesn't assert a specific authed value (that depends on
// whether the CI/dev environment happens to have `gh` authenticated or
// GITHUB_TOKEN set — see ghclient.GHAuthStatus), only that the endpoint
// never errors and always reports a boolean-shaped body.
func TestGitHubAuthStatus_ReturnsWellFormedResponse(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/github/auth-status", nil)
	w := httptest.NewRecorder()
	handlers.GitHubAuthStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Authed bool   `json:"authed"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
