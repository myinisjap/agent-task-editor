package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withFakeClaudeHome points $HOME at a temp dir for the duration of the
// test and optionally writes a ~/.claude/.credentials.json with the given
// access token (skipped entirely if token is "").
func withFakeClaudeHome(t *testing.T, token string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if token == "" {
		return
	}
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := `{"claudeAiOauth":{"accessToken":"` + token + `"}}`
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestFetchClaudeUsage_NoCredentials verifies that FetchClaudeUsage returns
// ErrNoClaudeCredentials (and never panics or hangs) when no OAuth
// credentials file is present.
func TestFetchClaudeUsage_NoCredentials(t *testing.T) {
	withFakeClaudeHome(t, "")

	usage, err := FetchClaudeUsage(context.Background())
	if usage != nil {
		t.Errorf("expected nil usage, got %+v", usage)
	}
	if !errors.Is(err, ErrNoClaudeCredentials) {
		t.Fatalf("expected ErrNoClaudeCredentials, got %v", err)
	}
}

// TestFetchClaudeUsage_ParsesFiveHourAndSevenDay verifies that a
// representative 200 response is parsed into ClaudeUsage with clamped
// percentages and parsed reset times, and that the request carries the
// expected auth/beta headers.
func TestFetchClaudeUsage_ParsesFiveHourAndSevenDay(t *testing.T) {
	withFakeClaudeHome(t, "test-token-123")

	var gotAuth, gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 42.5, "resets_at": "2026-07-03T18:00:00Z"},
			"seven_day": {"utilization": 150, "resets_at": "2026-07-10T00:00:00Z"}
		}`))
	}))
	defer srv.Close()

	old := claudeUsageBaseURL
	claudeUsageBaseURL = srv.URL
	defer func() { claudeUsageBaseURL = old }()

	usage, err := FetchClaudeUsage(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer test-token-123" {
		t.Errorf("expected Authorization header 'Bearer test-token-123', got %q", gotAuth)
	}
	if gotBeta != "oauth-2025-04-20" {
		t.Errorf("expected anthropic-beta header 'oauth-2025-04-20', got %q", gotBeta)
	}
	if usage.FiveHourPercent != 42.5 {
		t.Errorf("expected FiveHourPercent=42.5, got %v", usage.FiveHourPercent)
	}
	if usage.FiveHourResetsAt == nil || !usage.FiveHourResetsAt.Equal(time.Date(2026, 7, 3, 18, 0, 0, 0, time.UTC)) {
		t.Errorf("unexpected FiveHourResetsAt: %v", usage.FiveHourResetsAt)
	}
	// 150 should be clamped to 100.
	if usage.WeeklyPercent != 100 {
		t.Errorf("expected WeeklyPercent clamped to 100, got %v", usage.WeeklyPercent)
	}
	if usage.WeeklyResetsAt == nil || !usage.WeeklyResetsAt.Equal(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("unexpected WeeklyResetsAt: %v", usage.WeeklyResetsAt)
	}
}

// TestFetchClaudeUsage_NonOKStatus verifies that a non-200 response (e.g.
// 429 or 500) produces a descriptive error rather than a panic, and does
// not leak the access token into the error text.
func TestFetchClaudeUsage_NonOKStatus(t *testing.T) {
	withFakeClaudeHome(t, "super-secret-token")

	tests := []struct {
		name   string
		status int
	}{
		{"rate limited", http.StatusTooManyRequests},
		{"server error", http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"error":"nope"}`))
			}))
			defer srv.Close()

			old := claudeUsageBaseURL
			claudeUsageBaseURL = srv.URL
			defer func() { claudeUsageBaseURL = old }()

			usage, err := FetchClaudeUsage(context.Background())
			if usage != nil {
				t.Errorf("expected nil usage on error, got %+v", usage)
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if containsToken(err.Error(), "super-secret-token") {
				t.Errorf("error message must not leak the access token: %v", err)
			}
		})
	}
}

func containsToken(s, tok string) bool {
	return len(s) >= len(tok) && (func() bool {
		for i := 0; i+len(tok) <= len(s); i++ {
			if s[i:i+len(tok)] == tok {
				return true
			}
		}
		return false
	})()
}
