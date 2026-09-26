package remotemcp

import (
	"crypto/sha256"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
)

const (
	testClientID = "google-client"
	allowedEmail = "owner@example.com"
)

// fakeGoogle is a Google token endpoint that returns an ID token for whatever
// email the test puts in *email.
func fakeGoogle(t *testing.T, email *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("code") != "google-code" || r.PostForm.Get("client_secret") != "google-secret" {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		claims, _ := json.Marshal(map[string]any{
			"iss": "https://accounts.google.com", "aud": testClientID, "exp": 4102444800,
			"email": *email, "email_verified": true,
		})
		idToken := "e30." + b64.EncodeToString(claims) + ".sig"
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": idToken})
	}))
}

// fakeAPI stands in for the backend router: a bearer-protected /api/v1/tasks
// that echoes the resolved actor, so tests can see the in-process call got
// through BearerAuth as the signed-in user.
func fakeAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "t1", "title": "by " + middleware.ActorFromContext(r.Context()), "label": r.URL.Query().Get("label")}})
	})
	return middleware.BearerAuth("static-api-token", nil)(mux)
}

func newTestServer(t *testing.T, email *string) (http.Handler, *httptest.Server) {
	t.Helper()
	g := fakeGoogle(t, email)
	t.Cleanup(g.Close)
	s, err := New(Config{
		PublicURL:          "https://board.example.com/tasks/",
		GoogleClientID:     testClientID,
		GoogleClientSecret: "google-secret",
		AllowedEmails:      []string{"Owner@Example.com"},
		RedirectHosts:      []string{"claude.ai"},
		GoogleAuthURL:      "https://google.test/auth",
		GoogleTokenURL:     g.URL,
	}, fakeAPI())
	if err != nil {
		t.Fatal(err)
	}
	return s.Wrap(http.NotFoundHandler()), g
}

func do(h http.Handler, method, target, contentType, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func register(t *testing.T, h http.Handler, redirect string) (string, int) {
	t.Helper()
	rec := do(h, http.MethodPost, "/oauth/register", "application/json", `{"client_name":"Claude","redirect_uris":["`+redirect+`"]}`)
	var out struct {
		ClientID string `json:"client_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.ClientID, rec.Code
}

// authorize runs /authorize + the Google callback and returns the redirect the
// client receives.
func authorize(t *testing.T, h http.Handler, clientID, challenge string) *url.URL {
	t.Helper()
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"client-state"},
		"resource": {"https://board.example.com/tasks/mcp"},
	}
	g, cookie := consent(t, h, q)
	if g.Host != "google.test" || g.Query().Get("redirect_uri") != "https://board.example.com/tasks/oauth/callback" {
		t.Fatalf("unexpected Google redirect %s", g)
	}
	rec := do(h, http.MethodGet, "/oauth/callback?code=google-code&state="+url.QueryEscape(g.Query().Get("state")), "", "", "Cookie", cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body)
	}
	back, _ := url.Parse(rec.Header().Get("Location"))
	if back.Host != "claude.ai" || back.Query().Get("state") != "client-state" {
		t.Fatalf("unexpected client redirect %s", back)
	}
	return back
}

var continueLink = regexp.MustCompile(`href="([^"]+)"`)

// consent calls /authorize and returns the consent page's Google link and the
// browser-binding cookie it set (as a Cookie header value).
func consent(t *testing.T, h http.Handler, q url.Values) (*url.URL, string) {
	t.Helper()
	rec := do(h, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", "")
	m := continueLink.FindStringSubmatch(rec.Body.String())
	if rec.Code != http.StatusOK || m == nil {
		t.Fatalf("authorize: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("consent page can be framed")
	}
	g, _ := url.Parse(html.UnescapeString(m[1]))
	c := rec.Result().Cookies()
	if len(c) != 1 || !c[0].HttpOnly || !c[0].Secure || c[0].Path != "/tasks/oauth/" {
		t.Fatalf("binding cookie: %+v", c)
	}
	return g, c[0].Name + "=" + c[0].Value
}

func pkce(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return b64.EncodeToString(sum[:])
}

func token(h http.Handler, form url.Values) (map[string]any, int) {
	rec := do(h, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", form.Encode())
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out, rec.Code
}

func rpc(h http.Handler, access, body string) *httptest.ResponseRecorder {
	return do(h, http.MethodPost, "/mcp", "application/json", body, "Authorization", "Bearer "+access, "Accept", "application/json, text/event-stream")
}

func TestMetadata(t *testing.T) {
	email := allowedEmail
	h, _ := newTestServer(t, &email)

	rec := do(h, http.MethodGet, "/.well-known/oauth-protected-resource/tasks/mcp", "", "")
	if !strings.Contains(rec.Body.String(), `"resource":"https://board.example.com/tasks/mcp"`) {
		t.Errorf("resource metadata: %s", rec.Body)
	}
	rec = do(h, http.MethodGet, "/.well-known/oauth-authorization-server/tasks", "", "")
	if !strings.Contains(rec.Body.String(), `"token_endpoint":"https://board.example.com/tasks/oauth/token"`) {
		t.Errorf("server metadata: %s", rec.Body)
	}
	// Anything else falls through to the wrapped handler.
	if rec := do(h, http.MethodGet, "/api/v1/tasks", "", ""); rec.Code != http.StatusNotFound {
		t.Errorf("fallthrough: got %d", rec.Code)
	}
}

func TestMCPRequiresToken(t *testing.T) {
	email := allowedEmail
	h, _ := newTestServer(t, &email)
	rec := do(h, http.MethodPost, "/mcp", "application/json", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rec.Code)
	}
	want := `resource_metadata="https://board.example.com/.well-known/oauth-protected-resource/tasks/mcp"`
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, want) {
		t.Errorf("WWW-Authenticate = %q", got)
	}
	if rec := rpc(h, "ate1.forged.token", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("forged token: got %d", rec.Code)
	}
}

func TestRegisterRejectsForeignRedirect(t *testing.T) {
	email := allowedEmail
	h, _ := newTestServer(t, &email)
	if _, code := register(t, h, "https://evil.example/cb"); code != http.StatusBadRequest {
		t.Errorf("evil redirect: got %d", code)
	}
	if _, code := register(t, h, "http://claude.ai/cb"); code != http.StatusBadRequest {
		t.Errorf("plain-http redirect: got %d", code)
	}
	if _, code := register(t, h, "http://127.0.0.1:3333/callback"); code != http.StatusCreated {
		t.Errorf("loopback redirect: got %d", code)
	}
}

func TestFullFlow(t *testing.T) {
	email := allowedEmail
	h, _ := newTestServer(t, &email)

	clientID, code := register(t, h, "https://claude.ai/api/mcp/auth_callback")
	if code != http.StatusCreated {
		t.Fatalf("register: %d", code)
	}
	back := authorize(t, h, clientID, pkce("verifier-123"))

	// Wrong verifier burns the code.
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {back.Query().Get("code")},
		"redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "code_verifier": {"wrong"}}
	if _, status := token(h, form); status != http.StatusBadRequest {
		t.Fatalf("bad PKCE accepted: %d", status)
	}
	form.Set("code_verifier", "verifier-123")
	if _, status := token(h, form); status != http.StatusBadRequest {
		t.Fatalf("code reused: %d", status)
	}

	back = authorize(t, h, clientID, pkce("verifier-123"))
	form.Set("code", back.Query().Get("code"))
	tok, status := token(h, form)
	if status != http.StatusOK {
		t.Fatalf("token: %d %v", status, tok)
	}
	access := tok["access_token"].(string)

	rec := rpc(h, access, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	if !strings.Contains(rec.Body.String(), `"protocolVersion":"2025-03-26"`) {
		t.Errorf("initialize: %s", rec.Body)
	}
	if rec := rpc(h, access, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); rec.Code != http.StatusAccepted {
		t.Errorf("notification: got %d", rec.Code)
	}
	rec = rpc(h, access, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	for _, name := range []string{"create_task", "list_tasks", "reply_to_run", "approve_task", "move_task"} {
		if !strings.Contains(rec.Body.String(), `"name":"`+name+`"`) {
			t.Errorf("tools/list missing %s", name)
		}
	}

	// The tool call reaches the bearer-protected API in-process, as the user.
	rec = rpc(h, access, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_tasks","arguments":{"label":"review"}}}`)
	var resp struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Result.IsError || len(resp.Result.Content) == 0 ||
		!strings.Contains(resp.Result.Content[0].Text, "by owner@example.com") ||
		!strings.Contains(resp.Result.Content[0].Text, `"label":"review"`) {
		t.Errorf("tools/call: %s", rec.Body)
	}

	// Refresh issues a working access token; an access token can't refresh.
	refreshed, status := token(h, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {tok["refresh_token"].(string)}})
	if status != http.StatusOK {
		t.Fatalf("refresh: %d %v", status, refreshed)
	}
	if rec := rpc(h, refreshed["access_token"].(string), `{"jsonrpc":"2.0","id":4,"method":"ping"}`); rec.Code != http.StatusOK {
		t.Errorf("refreshed token rejected: %d", rec.Code)
	}
	if _, status := token(h, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {access}}); status != http.StatusBadRequest {
		t.Errorf("access token accepted as refresh token: %d", status)
	}
}

func TestDisallowedEmail(t *testing.T) {
	email := "stranger@example.com"
	h, _ := newTestServer(t, &email)
	clientID, _ := register(t, h, "https://claude.ai/api/mcp/auth_callback")
	back := authorize(t, h, clientID, pkce("v"))
	if back.Query().Get("error") != "access_denied" || back.Query().Get("code") != "" {
		t.Errorf("stranger got %s", back)
	}
}

// A client registered while a host was allowed can't be sent there once the
// host is removed from the allowlist.
func TestAuthorizeRechecksRedirectHost(t *testing.T) {
	email := allowedEmail
	g := fakeGoogle(t, &email)
	t.Cleanup(g.Close)
	cfg := Config{
		PublicURL: "https://board.example.com/tasks", GoogleClientID: testClientID, GoogleClientSecret: "google-secret",
		AllowedEmails: []string{allowedEmail}, RedirectHosts: []string{"claude.ai", "old.example"},
		GoogleAuthURL: "https://google.test/auth", GoogleTokenURL: g.URL,
	}
	s1, _ := New(cfg, http.NotFoundHandler())
	clientID, _ := register(t, s1.Wrap(http.NotFoundHandler()), "https://old.example/cb")

	cfg.RedirectHosts = []string{"claude.ai"}
	s2, _ := New(cfg, http.NotFoundHandler())
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"https://old.example/cb"},
		"code_challenge": {"x"}, "code_challenge_method": {"S256"}}
	rec := do(s2.Wrap(http.NotFoundHandler()), http.MethodGet, "/oauth/authorize?"+q.Encode(), "", "")
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
		t.Errorf("got %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
}

// A Google link lifted from someone else's /authorize can't be completed in a
// browser that doesn't hold that sign-in's binding cookie.
func TestCallbackRequiresBindingCookie(t *testing.T) {
	email := allowedEmail
	h, _ := newTestServer(t, &email)
	clientID, _ := register(t, h, "https://claude.ai/api/mcp/auth_callback")
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge": {pkce("v")}, "code_challenge_method": {"S256"}, "state": {"s"}}
	g, cookie := consent(t, h, q)
	state := url.QueryEscape(g.Query().Get("state"))

	rec := do(h, http.MethodGet, "/oauth/callback?code=google-code&state="+state, "", "")
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
		t.Fatalf("no cookie: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// The failed attempt also burned the pending sign-in.
	rec = do(h, http.MethodGet, "/oauth/callback?code=google-code&state="+state, "", "", "Cookie", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("replay after failure: %d", rec.Code)
	}
}

func TestAuthorizeLimits(t *testing.T) {
	email := allowedEmail
	h, _ := newTestServer(t, &email)
	clientID, _ := register(t, h, "https://claude.ai/api/mcp/auth_callback")
	base := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge_method": {"S256"}}

	for name, mod := range map[string]func(url.Values){
		"short challenge": func(q url.Values) { q.Set("code_challenge", "abc") },
		"long state": func(q url.Values) {
			q.Set("code_challenge", pkce("v"))
			q.Set("state", strings.Repeat("x", maxState+1))
		},
	} {
		q := url.Values{}
		for k, v := range base {
			q[k] = v
		}
		mod(q)
		rec := do(h, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", "")
		if loc, _ := url.Parse(rec.Header().Get("Location")); rec.Code != http.StatusFound || loc.Query().Get("error") != "invalid_request" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Header().Get("Location"))
		}
	}

	q := url.Values{}
	for k, v := range base {
		q[k] = v
	}
	q.Set("code_challenge", pkce("v"))
	for i := 0; i < maxPending; i++ {
		if rec := do(h, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", ""); rec.Code != http.StatusOK {
			t.Fatalf("authorize %d: %d", i, rec.Code)
		}
	}
	rec := do(h, http.MethodGet, "/oauth/authorize?"+q.Encode(), "", "")
	if loc, _ := url.Parse(rec.Header().Get("Location")); loc == nil || loc.Query().Get("error") != "temporarily_unavailable" {
		t.Errorf("over cap: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestDotSegmentsRejected(t *testing.T) {
	email := allowedEmail
	h, _ := newTestServer(t, &email)
	for _, p := range []string{"/oauth/../api/v1/repos", "/.well-known/oauth-protected-resource/../../api/v1/repos"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.URL.Path = p
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d", p, rec.Code)
		}
	}
}

func TestUnknownToolIsRPCError(t *testing.T) {
	email := allowedEmail
	h, _ := newTestServer(t, &email)
	clientID, _ := register(t, h, "https://claude.ai/api/mcp/auth_callback")
	back := authorize(t, h, clientID, pkce("v"))
	tok, _ := token(h, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {back.Query().Get("code")}, "code_verifier": {"v"}})
	rec := rpc(h, tok["access_token"].(string), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_everything"}}`)
	if !strings.Contains(rec.Body.String(), `"code":-32602`) {
		t.Errorf("got %s", rec.Body)
	}
}
