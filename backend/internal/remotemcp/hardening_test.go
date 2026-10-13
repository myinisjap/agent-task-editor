package remotemcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// newHardeningServer builds a server with cfg adjusted by mutate.
func newHardeningServer(t *testing.T, mutate func(*Config)) (http.Handler, Config) {
	t.Helper()
	email := allowedEmail
	g := fakeGoogle(t, &email)
	t.Cleanup(g.Close)
	cfg := testConfig(t, g.URL)
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := New(cfg, fakeAPI())
	if err != nil {
		t.Fatal(err)
	}
	return s.Wrap(http.NotFoundHandler()), cfg
}

// signIn registers a client and completes the flow, returning its id and tokens.
func signIn(t *testing.T, h http.Handler) (string, map[string]any) {
	t.Helper()
	clientID, code := register(t, h, "https://claude.ai/api/mcp/auth_callback")
	if code != http.StatusCreated {
		t.Fatalf("register: %d", code)
	}
	back := authorize(t, h, clientID, pkce("v"))
	tok, status := token(h, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {back.Query().Get("code")}, "code_verifier": {"v"}})
	if status != http.StatusOK {
		t.Fatalf("token: %d %v", status, tok)
	}
	return clientID, tok
}

func TestSigningKeyRequired(t *testing.T) {
	email := allowedEmail
	g := fakeGoogle(t, &email)
	t.Cleanup(g.Close)
	for _, key := range []string{"", "too-short"} {
		cfg := testConfig(t, g.URL)
		cfg.SigningKey = key
		_, err := New(cfg, http.NotFoundHandler())
		if err == nil || !strings.Contains(err.Error(), "MCP_SIGNING_KEY") {
			t.Errorf("key %q: err = %v, want MCP_SIGNING_KEY error", key, err)
		}
	}
}

// Tokens signed under one key are worthless under another, and the Google
// client secret plays no part in signing.
func TestSigningKeyIsIndependentOfGoogleSecret(t *testing.T) {
	email := allowedEmail
	g := fakeGoogle(t, &email)
	t.Cleanup(g.Close)
	cfg := testConfig(t, g.URL)
	a, _ := New(cfg, http.NotFoundHandler())
	cfg.GoogleClientSecret = "rotated-google-secret"
	b, _ := New(cfg, http.NotFoundHandler())
	cfg.SigningKey = "a-completely-different-signing-key-123456"
	c, _ := New(cfg, http.NotFoundHandler())
	tok := a.sign(claims{Typ: tokenClient})
	if _, err := b.verify(tok, tokenClient); err != nil {
		t.Errorf("rotating the Google secret invalidated tokens: %v", err)
	}
	if _, err := c.verify(tok, tokenClient); err == nil {
		t.Error("token verified under a different signing key")
	}
}

func TestToolProfiles(t *testing.T) {
	list := func(h http.Handler, access string) string {
		return rpc(h, access, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`).Body.String()
	}
	mutating := []string{"create_task", "reply_to_run", "approve_task", "reject_task", "move_task", "archive_task"}

	// Default is read-only.
	h, _ := newHardeningServer(t, func(c *Config) { c.ToolProfile = "" })
	_, tok := signIn(t, h)
	access := tok["access_token"].(string)
	out := list(h, access)
	if !strings.Contains(out, `"name":"list_tasks"`) {
		t.Errorf("readonly profile lost list_tasks: %s", out)
	}
	for _, n := range mutating {
		if strings.Contains(out, `"name":"`+n+`"`) {
			t.Errorf("readonly profile exposes %s", n)
		}
	}
	// Calling a hidden tool directly fails too.
	call := rpc(h, access, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"approve_task","arguments":{"task_id":"t1"}}}`).Body.String()
	if !strings.Contains(call, "unknown tool") {
		t.Errorf("readonly profile ran approve_task: %s", call)
	}

	// Full exposes everything.
	h, _ = newHardeningServer(t, func(c *Config) { c.ToolProfile = "FULL" })
	_, tok = signIn(t, h)
	out = list(h, tok["access_token"].(string))
	for _, n := range mutating {
		if !strings.Contains(out, `"name":"`+n+`"`) {
			t.Errorf("full profile missing %s", n)
		}
	}

	// Typos fail at startup rather than silently picking a profile.
	email := allowedEmail
	g := fakeGoogle(t, &email)
	t.Cleanup(g.Close)
	cfg := testConfig(t, g.URL)
	cfg.ToolProfile = "rw"
	if _, err := New(cfg, http.NotFoundHandler()); err == nil {
		t.Error("invalid profile accepted")
	}
}

func TestAllowedClientIDs(t *testing.T) {
	// Register under an open server, then pin that client id.
	open, _ := newHardeningServer(t, nil)
	clientID, _ := register(t, open, "https://claude.ai/api/mcp/auth_callback")
	other, _ := register(t, open, "https://claude.com/api/mcp/auth_callback")

	h, _ := newHardeningServer(t, func(c *Config) { c.AllowedClientIDs = []string{clientID} })
	q := url.Values{"response_type": {"code"}, "client_id": {other}, "redirect_uri": {"https://claude.com/api/mcp/auth_callback"},
		"code_challenge": {pkce("v")}, "code_challenge_method": {"S256"}}
	if rec := do(h, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("unlisted client authorized: %d", rec.Code)
	}
	if _, status := token(h, url.Values{"grant_type": {"refresh_token"}, "client_id": {other}, "refresh_token": {"x"}}); status != http.StatusUnauthorized {
		t.Errorf("unlisted client at /token: %d", status)
	}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", "https://claude.ai/api/mcp/auth_callback")
	if rec := do(h, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", ""); rec.Code != http.StatusOK {
		t.Errorf("listed client refused: %d %s", rec.Code, rec.Body)
	}
}

func TestRevokeGrant(t *testing.T) {
	ctx := context.Background()
	h, cfg := newHardeningServer(t, nil)
	clientID, tok := signIn(t, h)
	_, tok2 := signIn(t, h) // a second, independent session
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	refresh := func(tok map[string]any) int {
		_, status := token(h, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {tok["refresh_token"].(string)}})
		return status
	}

	grants, err := ListGrants(ctx, cfg.DB)
	if err != nil || len(grants) != 2 {
		t.Fatalf("ListGrants: %v %v", grants, err)
	}
	// Refreshing keeps the same grant, so there is still one row per sign-in.
	if refresh(tok) != http.StatusOK {
		t.Fatal("refresh before revoke failed")
	}
	if grants, _ = ListGrants(ctx, cfg.DB); len(grants) != 2 {
		t.Fatalf("refresh created a grant: %v", grants)
	}

	// Revoke only the first session.
	payload, err := b64.DecodeString(strings.Split(tok["access_token"].(string), ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	var cl claims
	if err := json.Unmarshal(payload, &cl); err != nil || cl.Jti == "" {
		t.Fatalf("access token has no grant id: %v %+v", err, cl)
	}
	first := cl.Jti
	ok, err := RevokeGrant(ctx, cfg.DB, first)
	if err != nil || !ok {
		t.Fatalf("RevokeGrant: %v %v", ok, err)
	}
	if again, _ := RevokeGrant(ctx, cfg.DB, first); again {
		t.Error("revoking twice reported success")
	}
	if rec := rpc(h, tok["access_token"].(string), ping); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked access token still works: %d", rec.Code)
	}
	if refresh(tok) != http.StatusBadRequest {
		t.Error("revoked refresh token still works")
	}
	if rec := rpc(h, tok2["access_token"].(string), ping); rec.Code != http.StatusOK {
		t.Errorf("other session was affected: %d", rec.Code)
	}

	// Revoking by email takes the rest.
	n, err := RevokeEmail(ctx, cfg.DB, "OWNER@example.com")
	if err != nil || n != 1 {
		t.Fatalf("RevokeEmail: %d %v", n, err)
	}
	if rec := rpc(h, tok2["access_token"].(string), ping); rec.Code != http.StatusUnauthorized {
		t.Errorf("access token survived revoke-by-email: %d", rec.Code)
	}
}

// Tokens without a grant id (or for an unknown one) are rejected even with a
// valid signature.
func TestTokenWithoutGrantRejected(t *testing.T) {
	email := allowedEmail
	g := fakeGoogle(t, &email)
	t.Cleanup(g.Close)
	cfg := testConfig(t, g.URL)
	s, err := New(cfg, fakeAPI())
	if err != nil {
		t.Fatal(err)
	}
	h := s.Wrap(http.NotFoundHandler())
	now := s.now()
	for _, jti := range []string{"", "no-such-grant"} {
		access := s.sign(claims{Typ: tokenAccess, Sub: allowedEmail, Jti: jti, Aud: s.resourceURL(), IssuedAt: now.Unix(), ExpiresAt: now.Unix() + 3600})
		if rec := rpc(h, access, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); rec.Code != http.StatusUnauthorized {
			t.Errorf("jti %q accepted: %d", jti, rec.Code)
		}
	}
}
