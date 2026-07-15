package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// writeCredsFile writes a ~/.claude/.credentials.json under the given home
// dir with the given oauth fields plus some extra fields we must preserve.
func writeCredsFile(t *testing.T, home, accessToken, refreshToken string, expiresAt int64) string {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".credentials.json")
	content := map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":      accessToken,
			"refreshToken":     refreshToken,
			"expiresAt":        expiresAt,
			"scopes":           []string{"user:inference", "user:profile"},
			"subscriptionType": "pro",
			"rateLimitTier":    "default_claude_ai",
		},
		"otherTopLevel": map[string]any{"keep": true},
	}
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// withFixedNow pins nowFunc for the duration of the test.
func withFixedNow(t *testing.T, now time.Time) {
	t.Helper()
	prev := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = prev })
}

// withTokenEndpoint points claudeTokenEndpoint at a test server.
func withTokenEndpoint(t *testing.T, url string) {
	t.Helper()
	prev := claudeTokenEndpoint
	claudeTokenEndpoint = url
	t.Cleanup(func() { claudeTokenEndpoint = prev })
}

// TestClaudeOAuthToken_ValidTokenReturnedWithoutRefresh verifies a token that
// is comfortably within its validity window is returned as-is and no refresh
// request is made.
func TestClaudeOAuthToken_ValidTokenReturnedWithoutRefresh(t *testing.T) {
	now := time.Now()
	withFixedNow(t, now)
	home := t.TempDir()
	writeCredsFile(t, home, "tok-valid", "refresh-1", now.Add(2*time.Hour).UnixMilli())

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withTokenEndpoint(t, srv.URL)

	got := claudeOAuthAccessTokenFrom(home)
	if got != "tok-valid" {
		t.Fatalf("expected tok-valid, got %q", got)
	}
	if called {
		t.Fatal("refresh endpoint should not have been called for a valid token")
	}
}

// TestClaudeOAuthToken_ExpiredTokenIsRefreshedAndPersisted verifies an
// expired token triggers a refresh, the new token is returned, and the
// rotated tokens are written back to the credentials file with all other
// fields preserved.
func TestClaudeOAuthToken_ExpiredTokenIsRefreshedAndPersisted(t *testing.T) {
	now := time.Now()
	withFixedNow(t, now)
	home := t.TempDir()
	path := writeCredsFile(t, home, "tok-old", "refresh-old", now.Add(-time.Minute).UnixMilli())

	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode refresh request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "tok-new",
			"refresh_token": "refresh-new",
			"expires_in":    28800,
		})
	}))
	defer srv.Close()
	withTokenEndpoint(t, srv.URL)

	got := claudeOAuthAccessTokenFrom(home)
	if got != "tok-new" {
		t.Fatalf("expected refreshed token tok-new, got %q", got)
	}
	if gotBody["grant_type"] != "refresh_token" || gotBody["refresh_token"] != "refresh-old" || gotBody["client_id"] != claudeOAuthClientID {
		t.Fatalf("unexpected refresh request body: %v", gotBody)
	}

	// The credentials file must now carry the rotated tokens and the new
	// expiry, and preserve every field we don't own.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["otherTopLevel"]; !ok {
		t.Fatal("sibling top-level key was dropped on rewrite")
	}
	var oauth map[string]any
	if err := json.Unmarshal(top["claudeAiOauth"], &oauth); err != nil {
		t.Fatal(err)
	}
	if oauth["accessToken"] != "tok-new" || oauth["refreshToken"] != "refresh-new" {
		t.Fatalf("tokens not persisted: %v", oauth)
	}
	wantExpiry := float64(now.Add(28800 * time.Second).UnixMilli())
	if oauth["expiresAt"] != wantExpiry {
		t.Fatalf("expiresAt not persisted: got %v want %v", oauth["expiresAt"], wantExpiry)
	}
	if oauth["subscriptionType"] != "pro" {
		t.Fatal("subscriptionType was dropped on rewrite")
	}
	if _, ok := oauth["scopes"]; !ok {
		t.Fatal("scopes were dropped on rewrite")
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("credentials file perms = %o, want 600", perm)
		}
	}
}

// TestClaudeOAuthToken_ExpiringSoonIsRefreshedProactively verifies a token
// inside the refresh-skew window (but not yet expired) is refreshed.
func TestClaudeOAuthToken_ExpiringSoonIsRefreshedProactively(t *testing.T) {
	now := time.Now()
	withFixedNow(t, now)
	home := t.TempDir()
	writeCredsFile(t, home, "tok-old", "refresh-old", now.Add(time.Minute).UnixMilli())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "tok-new",
			"refresh_token": "refresh-new",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()
	withTokenEndpoint(t, srv.URL)

	if got := claudeOAuthAccessTokenFrom(home); got != "tok-new" {
		t.Fatalf("expected proactive refresh to return tok-new, got %q", got)
	}
}

// TestClaudeOAuthToken_ProactiveRefreshFailureKeepsCurrentToken verifies that
// when the token is expiring soon but still valid and the refresh fails, the
// current (still-valid) token is returned rather than "".
func TestClaudeOAuthToken_ProactiveRefreshFailureKeepsCurrentToken(t *testing.T) {
	now := time.Now()
	withFixedNow(t, now)
	home := t.TempDir()
	writeCredsFile(t, home, "tok-current", "refresh-1", now.Add(time.Minute).UnixMilli())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	withTokenEndpoint(t, srv.URL)

	if got := claudeOAuthAccessTokenFrom(home); got != "tok-current" {
		t.Fatalf("expected still-valid current token, got %q", got)
	}
}

// TestClaudeOAuthToken_ExpiredAndUnrefreshableReturnsEmpty verifies an
// expired token whose refresh fails yields "" (so the claude CLI subprocess
// is launched without a stale ANTHROPIC_AUTH_TOKEN and can run its own
// refresh flow).
func TestClaudeOAuthToken_ExpiredAndUnrefreshableReturnsEmpty(t *testing.T) {
	now := time.Now()
	withFixedNow(t, now)
	home := t.TempDir()
	writeCredsFile(t, home, "tok-stale", "refresh-bad", now.Add(-time.Hour).UnixMilli())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	withTokenEndpoint(t, srv.URL)

	if got := claudeOAuthAccessTokenFrom(home); got != "" {
		t.Fatalf("expected empty token for expired+unrefreshable creds, got %q", got)
	}
}

// TestClaudeOAuthToken_NoExpiryReturnsTokenAsIs verifies a credentials file
// without expiresAt is used as-is (no refresh attempted).
func TestClaudeOAuthToken_NoExpiryReturnsTokenAsIs(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"claudeAiOauth":{"accessToken":"tok-no-expiry"}}`
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := claudeOAuthAccessTokenFrom(home); got != "tok-no-expiry" {
		t.Fatalf("expected tok-no-expiry, got %q", got)
	}
}

// TestClaudeOAuthToken_MissingFileReturnsEmpty verifies the no-credentials
// case degrades to "".
func TestClaudeOAuthToken_MissingFileReturnsEmpty(t *testing.T) {
	if got := claudeOAuthAccessTokenFrom(t.TempDir()); got != "" {
		t.Fatalf("expected empty token for missing credentials file, got %q", got)
	}
}
