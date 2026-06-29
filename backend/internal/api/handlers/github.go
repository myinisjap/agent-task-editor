package handlers

import (
	"net/http"

	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
)

// GitHubAuthStatus reports whether gh CLI auth is available.
// The frontend calls this on load to show a warning if credentials are missing.
func GitHubAuthStatus(w http.ResponseWriter, r *http.Request) {
	authed, note := ghclient.GHAuthStatus()
	JSON(w, http.StatusOK, map[string]any{
		"authed": authed,
		"note":   note,
	})
}
