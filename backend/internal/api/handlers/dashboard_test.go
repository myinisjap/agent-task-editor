package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestDashboardGet_ClaudeUsageUnavailableWithoutCredentials verifies the
// dashboard endpoint still returns 200 and reports claude_usage.available
// == false when no Claude OAuth credentials are present in the environment
// (the common CI/test case) — the live Anthropic usage fetch must never
// fail or block the overall /dashboard response.
func TestDashboardGet_ClaudeUsageUnavailableWithoutCredentials(t *testing.T) {
	// Point HOME somewhere without a ~/.claude/.credentials.json so
	// agent.ClaudeOAuthAccessToken() reliably returns "".
	t.Setenv("HOME", t.TempDir())
	_ = os.Unsetenv("ANTHROPIC_AUTH_TOKEN")

	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewDashboardHandler(q)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		ClaudeUsage struct {
			Available       bool    `json:"available"`
			FiveHourPercent float64 `json:"five_hour_percent"`
			WeeklyPercent   float64 `json:"weekly_percent"`
		} `json:"claude_usage"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ClaudeUsage.Available {
		t.Errorf("expected claude_usage.available=false without credentials, got true")
	}
	if body.ClaudeUsage.FiveHourPercent != 0 || body.ClaudeUsage.WeeklyPercent != 0 {
		t.Errorf("expected zero-value percentages when unavailable, got %+v", body.ClaudeUsage)
	}
}
