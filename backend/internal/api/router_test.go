package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent/runtime"
	"github.com/myinisjap/agent-task-editor/backend/internal/api"
	"github.com/myinisjap/agent-task-editor/backend/internal/scopedtoken"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
	"nhooyr.io/websocket"
)

func openRouterTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp("", "router-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := storage.SeedDefaultWorkflow(context.Background(), db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

func newTestRouter(t *testing.T, apiToken, metricsToken string) http.Handler {
	t.Helper()
	db := openRouterTestDB(t)
	hub := ws.NewHub()
	engine := workflow.New(db.SQL(), hub)
	return api.NewRouter(db, engine, hub, "*", apiToken, nil, "", t.TempDir(), "", "", "", "", 24*time.Hour, 7, nil, nil, metricsToken, "dev", false, nil, 5, nil, nil, "*")
}

// TestHealthzEndpoint_UnauthenticatedEvenWithAPIToken verifies GET /healthz
// is reachable without a bearer token even when API_TOKEN is configured —
// it's mounted outside the BearerAuth group so container orchestrators
// (docker/k8s) can healthcheck without needing the token. See docs/api.md.
func TestHealthzEndpoint_UnauthenticatedEvenWithAPIToken(t *testing.T) {
	r := newTestRouter(t, "api-secret", "")

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("expected status %q, got %q", "ok", body.Status)
	}
}

// TestMetricsEndpoint_UnauthenticatedByDefault verifies GET /metrics is
// reachable without a bearer token when METRICS_TOKEN is unset, even though
// API_TOKEN is configured — the two are independent.
func TestMetricsEndpoint_UnauthenticatedByDefault(t *testing.T) {
	r := newTestRouter(t, "api-secret", "")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct == "" {
		t.Errorf("expected a Content-Type header")
	}
}

// TestMetricsEndpoint_GatedByMetricsToken verifies METRICS_TOKEN independently
// gates /metrics, separate from API_TOKEN.
func TestMetricsEndpoint_GatedByMetricsToken(t *testing.T) {
	r := newTestRouter(t, "", "metrics-secret")

	// No token: rejected.
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", w.Code)
	}

	// Correct token: allowed.
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer metrics-secret")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct token, got %d", w.Code)
	}
}

// apiV1AuthProbePaths is a representative sample of routes mounted under
// /api/v1 (inside the BearerAuth r.Group in NewRouter), used by the auth
// tests below to assert the whole group is actually gated end to end rather
// than trusting that each handler individually. GET /api/v1/backup is
// included deliberately: it streams a full DB snapshot (secrets included —
// see docs/backup.md), so proving it requires a token is the single most
// important assertion in this file.
var apiV1AuthProbePaths = []string{
	"/api/v1/tasks",
	"/api/v1/agents",
	"/api/v1/provider-configs",
	"/api/v1/backup",
}

// TestAPIv1_RequiresBearerToken verifies every route in apiV1AuthProbePaths
// — including GET /api/v1/backup, a full-DB-dump endpoint — rejects requests
// with no (or the wrong) bearer token with 401 when API_TOKEN is configured,
// and does not reject them when the correct token is supplied. Only "not
// 401" is asserted for the authorized case (rather than 200): some of these
// routes may 404/produce other codes with the nil dependencies newTestRouter
// wires up, but that's a different concern from auth — see newTestRouter's
// doc comment on why /tasks, /agents, and /provider-configs are safe to
// exercise here.
func TestAPIv1_RequiresBearerToken(t *testing.T) {
	r := newTestRouter(t, "api-secret", "")

	for _, path := range apiV1AuthProbePaths {
		t.Run("no token: "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected 401 with no token, got %d: %s", w.Code, w.Body.String())
			}
		})

		t.Run("wrong token: "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer wrong-secret")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected 401 with wrong token, got %d: %s", w.Code, w.Body.String())
			}
		})

		t.Run("correct token: "+path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer api-secret")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code == http.StatusUnauthorized {
				t.Errorf("expected non-401 with correct token, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestAPIv1_OpenWhenNoTokenConfigured verifies the documented "no API_TOKEN
// configured = open" behavior still holds for /api/v1 routes (matches
// middleware.BearerAuth's contract: an empty configured token disables the
// check entirely, rather than rejecting everything).
func TestAPIv1_OpenWhenNoTokenConfigured(t *testing.T) {
	r := newTestRouter(t, "", "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("expected /api/v1/agents to be reachable with no token configured, got 401: %s", w.Body.String())
	}
}

// fakeRouterTerminal is a no-op handlers.Terminal so the router mounts a
// non-nil terminal (ChatHandler.Terminal 503s before any auth check otherwise).
type fakeRouterTerminal struct{}

func (fakeRouterTerminal) Attach(ctx context.Context, sessionID, repoPath, provider, model string, resume bool, repoID string, pins []runtime.Pin, conn *websocket.Conn) error {
	return nil
}
func (fakeRouterTerminal) Stop(string) {}

func newWSTestRouter(t *testing.T, apiToken string, namedTokens map[string]string) (http.Handler, *ws.Hub) {
	t.Helper()
	db := openRouterTestDB(t)
	hub := ws.NewHub()
	engine := workflow.New(db.SQL(), hub)
	r := api.NewRouter(db, engine, hub, "*", apiToken, namedTokens, "", t.TempDir(), "", "", "", "", 24*time.Hour, 7, nil, nil, "", "dev", false, fakeRouterTerminal{}, 5, nil, nil, "")
	return r, hub
}

// TestChatTerminal_ThroughRouter_TicketNotBlockedByBearerAuth guards against
// the terminal route being mounted inside BearerAuth: browsers can't send an
// Authorization header on a WS upgrade, so a valid ?ticket= must reach the
// handler (which then 404s on the unknown session) rather than 401 in
// middleware.
func TestChatTerminal_ThroughRouter_TicketNotBlockedByBearerAuth(t *testing.T) {
	for name, tc := range map[string]struct {
		apiToken string
		named    map[string]string
	}{
		"API_TOKEN":  {apiToken: "api-secret"},
		"API_TOKENS": {named: map[string]string{"alice": "alice-secret"}},
	} {
		t.Run(name, func(t *testing.T) {
			r, hub := newWSTestRouter(t, tc.apiToken, tc.named)
			const path = "/api/v1/chat/sessions/nope/terminal"

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusUnauthorized {
				t.Errorf("no ticket: expected 401, got %d", w.Code)
			}

			ticket, err := hub.IssueTicket()
			if err != nil {
				t.Fatal(err)
			}
			w = httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path+"?ticket="+ticket, nil))
			if w.Code != http.StatusNotFound {
				t.Errorf("valid ticket: expected handler 404 (session not found), got %d: %s", w.Code, w.Body.String())
			}

			// Single-use: replay is rejected.
			w = httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path+"?ticket="+ticket, nil))
			if w.Code != http.StatusUnauthorized {
				t.Errorf("replayed ticket: expected 401, got %d", w.Code)
			}
		})
	}
}

// TestWS_ThroughRouter_NamedTokensOnly_RequiresAuth verifies /ws is not left
// open when only API_TOKENS (no legacy API_TOKEN) is configured.
func TestWS_ThroughRouter_NamedTokensOnly_RequiresAuth(t *testing.T) {
	r, hub := newWSTestRouter(t, "", map[string]string{"alice": "alice-secret"})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if c, _, err := websocket.Dial(ctx, wsURL, nil); err == nil {
		_ = c.Close(websocket.StatusNormalClosure, "")
		t.Fatal("unauthenticated /ws dial succeeded with API_TOKENS configured")
	}

	ticket, err := hub.IssueTicket()
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticket, nil)
	if err != nil {
		t.Fatalf("ticketed /ws dial failed: %v", err)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")

	// Deprecated ?token= fallback accepts a named token.
	c, _, err = websocket.Dial(ctx, wsURL+"?token=alice-secret", nil)
	if err != nil {
		t.Fatalf("named ?token= dial failed: %v", err)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
}

func TestAPIv1_ScopedSidecarTokens(t *testing.T) {
	h := newTestRouter(t, "admin-tok", "")
	do := func(method, path, tok string) int {
		req := httptest.NewRequest(method, path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	run, err := scopedtoken.Default.Issue(scopedtoken.Grant{Kind: scopedtoken.KindRun, RunID: "r1", TaskID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	chat, _ := scopedtoken.Default.Issue(scopedtoken.Grant{Kind: scopedtoken.KindChat, Chat: "s1"})

	// Allowed: own task's subtasks route (handler may 4xx, but never 401/403).
	if c := do(http.MethodPost, "/api/v1/tasks/task-1/subtasks", run); c == http.StatusUnauthorized || c == http.StatusForbidden {
		t.Errorf("run token on own subtasks route: got %d", c)
	}
	// Rejected: other task, admin routes, wrong method, encoded path.
	for _, tc := range []struct{ m, p string }{
		{http.MethodPost, "/api/v1/tasks/task-2/subtasks"},
		{http.MethodGet, "/api/v1/tasks/task-1/subtasks"},
		{http.MethodGet, "/api/v1/backup"},
		{http.MethodPut, "/api/v1/agents/abc"},
		{http.MethodGet, "/api/v1/tasks"},
		{http.MethodPost, "/api/v1/tasks"},
		{http.MethodPost, "/api/v1/tasks/task-1%2Fx/subtasks"},
	} {
		if c := do(tc.m, tc.p, run); c != http.StatusForbidden {
			t.Errorf("run token %s %s: want 403, got %d", tc.m, tc.p, c)
		}
	}
	// Chat token: board routes only.
	for _, tc := range []struct{ m, p string }{
		{http.MethodGet, "/api/v1/repos"},
		{http.MethodGet, "/api/v1/workflows"},
		{http.MethodPost, "/api/v1/tasks"},
	} {
		if c := do(tc.m, tc.p, chat); c == http.StatusUnauthorized || c == http.StatusForbidden {
			t.Errorf("chat token %s %s: got %d", tc.m, tc.p, c)
		}
	}
	for _, tc := range []struct{ m, p string }{
		{http.MethodGet, "/api/v1/backup"},
		{http.MethodPut, "/api/v1/agents/abc"},
		{http.MethodPost, "/api/v1/tasks/task-1/subtasks"},
		{http.MethodDelete, "/api/v1/tasks/task-1"},
	} {
		if c := do(tc.m, tc.p, chat); c != http.StatusForbidden {
			t.Errorf("chat token %s %s: want 403, got %d", tc.m, tc.p, c)
		}
	}

	// Revoked tokens are plain 401.
	scopedtoken.Default.Revoke(run)
	scopedtoken.Default.Revoke(chat)
	if c := do(http.MethodPost, "/api/v1/tasks/task-1/subtasks", run); c != http.StatusUnauthorized {
		t.Errorf("revoked run token: want 401, got %d", c)
	}
	if c := do(http.MethodGet, "/api/v1/repos", chat); c != http.StatusUnauthorized {
		t.Errorf("revoked chat token: want 401, got %d", c)
	}
}

func newGuardedRouter(t *testing.T, apiToken, cors, allowedHosts string) http.Handler {
	t.Helper()
	db := openRouterTestDB(t)
	hub := ws.NewHub()
	engine := workflow.New(db.SQL(), hub)
	return api.NewRouter(db, engine, hub, cors, apiToken, nil, "", t.TempDir(), "", "", "", "", 24*time.Hour, 7, nil, nil, "", "dev", false, nil, 5, nil, nil, allowedHosts)
}

// TestCrossOriginTextPlainPost_DoesNotReachHandler is the drive-by-browser
// regression: a cross-site text/plain "simple" POST (no preflight) must be
// rejected before the handler runs, so no repo/task/agent is created.
func TestCrossOriginTextPlainPost_DoesNotReachHandler(t *testing.T) {
	r := newGuardedRouter(t, "", "http://localhost:5173", "")

	post := func(origin, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"title":"pwn","repo_id":"x"}`))
		req.Host = "localhost:8080"
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	if w := post("https://evil.example", "text/plain"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin text/plain: want 403, got %d: %s", w.Code, w.Body.String())
	}
	// Same-origin attacker origin spoofing nothing: the Content-Type guard alone also blocks it.
	if w := post("http://localhost:5173", "text/plain"); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("allowed origin text/plain: want 415, got %d: %s", w.Code, w.Body.String())
	}
	// Control: an allowed origin with JSON reaches the handler (which rejects the bogus repo itself, not 403/415).
	if w := post("http://localhost:5173", "application/json"); w.Code == http.StatusForbidden || w.Code == http.StatusUnsupportedMediaType {
		t.Fatalf("allowed origin JSON should reach handler, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPreflightStillWorksWithGuards(t *testing.T) {
	r := newGuardedRouter(t, "", "http://localhost:5173", "")
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/tasks", nil)
	req.Host = "localhost:8080"
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight: want 204, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("preflight ACAO = %q", got)
	}
}

func TestHostCheck_RouterLevel(t *testing.T) {
	get := func(r http.Handler, path, host string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = host
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	// No token: rebinding host rejected everywhere incl. /healthz, /metrics, /ws.
	r := newGuardedRouter(t, "", "http://localhost:5173", "")
	for _, p := range []string{"/api/v1/repos", "/healthz", "/metrics", "/ws"} {
		if c := get(r, p, "evil.example:8080"); c != http.StatusForbidden {
			t.Errorf("%s with evil host: want 403, got %d", p, c)
		}
	}
	if c := get(r, "/healthz", "localhost:8080"); c != http.StatusOK {
		t.Errorf("healthz on localhost: got %d", c)
	}
	// ALLOWED_HOSTS admits an extra name.
	r = newGuardedRouter(t, "", "http://localhost:5173", "ate.lan")
	if c := get(r, "/healthz", "ate.lan:8080"); c != http.StatusOK {
		t.Errorf("ALLOWED_HOSTS host: got %d", c)
	}
	// Token configured: Host check off (auth still applies).
	r = newGuardedRouter(t, "secret", "http://localhost:5173", "")
	if c := get(r, "/healthz", "ate.public.example"); c != http.StatusOK {
		t.Errorf("healthz with token + public host: got %d", c)
	}
	if c := get(r, "/api/v1/repos", "ate.public.example"); c != http.StatusUnauthorized {
		t.Errorf("api with token + public host and no bearer: want 401, got %d", c)
	}
}
