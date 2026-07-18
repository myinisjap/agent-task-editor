package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// claudeTokenEndpoint is Anthropic's OAuth token endpoint used to refresh an
// expired/expiring access token with the stored refresh token — the same
// endpoint Claude Code itself uses. Overridable in tests.
var claudeTokenEndpoint = "https://console.anthropic.com/v1/oauth/token"

// claudeOAuthClientID is Claude Code's public OAuth client ID (it ships in
// the CLI itself; it is not a secret).
const claudeOAuthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// claudeTokenRefreshSkew is how long before expiry we proactively refresh the
// access token, so a token that would expire mid-run is renewed up front.
const claudeTokenRefreshSkew = 5 * time.Minute

// claudeTokenHTTPClient is a short-timeout client so a hung token endpoint
// never stalls a run dispatch or dashboard load.
var claudeTokenHTTPClient = &http.Client{Timeout: 15 * time.Second}

// claudeCredsMu serializes the read-check-refresh-write cycle on
// ~/.claude/.credentials.json. Anthropic rotates the refresh token on use, so
// two concurrent refreshes would race: the loser's rotated-away refresh token
// becomes invalid and the account effectively gets logged out.
var claudeCredsMu sync.Mutex

// nowFunc is the clock, overridable in tests.
var nowFunc = time.Now

// claudeOAuthCreds is the subset of ~/.claude/.credentials.json's
// claudeAiOauth object we act on. ExpiresAt is epoch milliseconds.
type claudeOAuthCreds struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
}

// ClaudeOAuthAccessToken returns a currently-valid OAuth access token from
// ~/.claude/.credentials.json (written by `claude login` for Claude Max/Pro
// accounts), refreshing it via Anthropic's OAuth token endpoint when it is
// expired or about to expire, and persisting the rotated tokens back to the
// credentials file so this app and Claude Code stay in sync.
//
// Returns "" on any read/parse failure, if the file/field is absent, or if
// the token is expired and could not be refreshed — callers (the claude
// subprocess env injection, the dashboard usage widget) treat that as "no
// OAuth credentials available" and fall back gracefully. In particular the
// claude subprocess is then launched *without* ANTHROPIC_AUTH_TOKEN, letting
// the CLI run its own refresh flow against the credentials file directly.
func ClaudeOAuthAccessToken() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return claudeOAuthAccessTokenFrom(home)
}

// claudeOAuthAccessTokenFrom is the testable core of ClaudeOAuthAccessToken,
// accepting an explicit home directory.
func claudeOAuthAccessTokenFrom(home string) string {
	claudeCredsMu.Lock()
	defer claudeCredsMu.Unlock()

	path := filepath.Join(home, ".claude", ".credentials.json")
	creds, err := readClaudeOAuthCreds(path)
	if err != nil || creds.AccessToken == "" {
		return ""
	}

	// No expiry recorded — nothing to check, use the token as-is.
	if creds.ExpiresAt <= 0 {
		return creds.AccessToken
	}

	now := nowFunc()
	expiry := time.UnixMilli(creds.ExpiresAt)
	if now.Before(expiry.Add(-claudeTokenRefreshSkew)) {
		return creds.AccessToken // still comfortably valid
	}

	// Expired or expiring within the skew window — try to refresh.
	if creds.RefreshToken == "" {
		if now.Before(expiry) {
			return creds.AccessToken // can't refresh, but not expired yet
		}
		slog.Warn("claude oauth: access token expired and no refresh token available", "component", "agent")
		return ""
	}

	refreshed, err := refreshClaudeOAuthToken(creds.RefreshToken)
	if err != nil {
		slog.Warn("claude oauth: token refresh failed", "component", "agent", "error", err)
		if now.Before(expiry) {
			return creds.AccessToken // proactive refresh failed; current token still valid
		}
		return "" // expired and unrefreshable — let the claude CLI try its own flow
	}

	if err := writeClaudeOAuthCreds(path, refreshed); err != nil {
		// Refresh succeeded but persisting failed. The rotated refresh token
		// is now the only valid one and we just lost it — surface loudly.
		slog.Error("claude oauth: refreshed token but failed to persist credentials file", "component", "agent", "error", err)
	} else {
		slog.Info("claude oauth: access token refreshed", "component", "agent", "expires_at", time.UnixMilli(refreshed.ExpiresAt).Format(time.RFC3339))
	}
	return refreshed.AccessToken
}

// readClaudeOAuthCreds parses the fields we need from the credentials file.
func readClaudeOAuthCreds(path string) (claudeOAuthCreds, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return claudeOAuthCreds{}, err
	}
	var parsed struct {
		ClaudeAiOauth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return claudeOAuthCreds{}, err
	}
	return claudeOAuthCreds{
		AccessToken:  parsed.ClaudeAiOauth.AccessToken,
		RefreshToken: parsed.ClaudeAiOauth.RefreshToken,
		ExpiresAt:    parsed.ClaudeAiOauth.ExpiresAt,
	}, nil
}

// refreshClaudeOAuthToken exchanges a refresh token for a new access token
// (and rotated refresh token) at Anthropic's OAuth token endpoint.
func refreshClaudeOAuthToken(refreshToken string) (claudeOAuthCreds, error) {
	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     claudeOAuthClientID,
	})
	if err != nil {
		return claudeOAuthCreds{}, fmt.Errorf("marshal refresh request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, claudeTokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return claudeOAuthCreds{}, fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := claudeTokenHTTPClient.Do(req)
	if err != nil {
		return claudeOAuthCreds{}, fmt.Errorf("refresh request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return claudeOAuthCreds{}, fmt.Errorf("refresh request failed: http %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"` // seconds
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return claudeOAuthCreds{}, fmt.Errorf("decode refresh response: %w", err)
	}
	if parsed.AccessToken == "" {
		return claudeOAuthCreds{}, fmt.Errorf("refresh response had no access_token")
	}
	out := claudeOAuthCreds{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
	}
	if parsed.ExpiresIn > 0 {
		out.ExpiresAt = nowFunc().Add(time.Duration(parsed.ExpiresIn) * time.Second).UnixMilli()
	}
	return out, nil
}

// writeClaudeOAuthCreds merges the refreshed tokens back into the credentials
// file, preserving every field it does not own (scopes, subscriptionType,
// rateLimitTier, any future keys, and any sibling top-level objects). The
// write is atomic (temp file + rename) with 0600 permissions so a concurrent
// reader (Claude Code itself) never sees a partial file.
func writeClaudeOAuthCreds(path string, creds claudeOAuthCreds) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read credentials file: %w", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("parse credentials file: %w", err)
	}
	var oauth map[string]json.RawMessage
	if raw, ok := top["claudeAiOauth"]; ok {
		if err := json.Unmarshal(raw, &oauth); err != nil {
			return fmt.Errorf("parse claudeAiOauth: %w", err)
		}
	}
	if oauth == nil {
		oauth = map[string]json.RawMessage{}
	}
	set := func(key string, v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		oauth[key] = raw
		return nil
	}
	if err := set("accessToken", creds.AccessToken); err != nil {
		return err
	}
	if creds.RefreshToken != "" {
		if err := set("refreshToken", creds.RefreshToken); err != nil {
			return err
		}
	}
	if creds.ExpiresAt > 0 {
		if err := set("expiresAt", creds.ExpiresAt); err != nil {
			return err
		}
	}
	rawOauth, err := json.Marshal(oauth)
	if err != nil {
		return err
	}
	top["claudeAiOauth"] = rawOauth
	out, err := json.Marshal(top)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp credentials file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after successful rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp credentials file: %w", err)
	}
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp credentials file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp credentials file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename credentials file: %w", err)
	}
	return nil
}
