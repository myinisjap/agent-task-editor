package api_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/api"
	"github.com/myinisjap/agent-task-editor/backend/internal/ghsync"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

// authSmokeRoute is one representative route of an auth-sensitive group.
type authSmokeRoute struct {
	group  string
	method string
	path   string
	body   string
}

// bearerGatedRoutes lists a representative route per group mounted inside the
// BearerAuth group in router.go. Only 401 vs not-401 is asserted; the
// handlers run against an empty DB so other status codes are incidental.
var bearerGatedRoutes = []authSmokeRoute{
	{"tasks", http.MethodGet, "/api/v1/tasks", ""},
	{"tasks", http.MethodPost, "/api/v1/tasks/bulk", `{"ids":[],"action":"archive"}`},
	{"tasks", http.MethodPatch, "/api/v1/tasks/nope/label", `{"to_label":"done"}`},
	{"task runs", http.MethodGet, "/api/v1/tasks/nope/runs", ""},
	{"task label history", http.MethodGet, "/api/v1/tasks/nope/label-history", ""},
	{"side-effects retry", http.MethodPost, "/api/v1/tasks/nope/side-effects/retry", `{}`},
	{"dependencies", http.MethodGet, "/api/v1/tasks/nope/dependencies", ""},
	{"subtasks", http.MethodPost, "/api/v1/tasks/nope/subtasks", `{}`},
	{"review comments", http.MethodGet, "/api/v1/tasks/nope/review-comments", ""},
	{"uploads", http.MethodGet, "/api/v1/uploads/nope/nope.png", ""},
	{"repos", http.MethodGet, "/api/v1/repos", ""},
	{"repos", http.MethodPost, "/api/v1/repos", `{}`},
	{"workflows", http.MethodGet, "/api/v1/workflows", ""},
	{"workflows", http.MethodPost, "/api/v1/workflows/import", `{}`},
	{"agent configs", http.MethodGet, "/api/v1/agents", ""},
	{"agent configs", http.MethodPost, "/api/v1/agents", `{}`},
	{"provider configs", http.MethodGet, "/api/v1/provider-configs", ""},
	{"provider configs", http.MethodPost, "/api/v1/provider-configs", `{}`},
	{"templates", http.MethodGet, "/api/v1/templates", ""},
	{"repo lessons list", http.MethodGet, "/api/v1/repos/nope/lessons", ""},
	{"repo lessons create", http.MethodPost, "/api/v1/repos/nope/lessons", `{}`},
	{"global lessons list", http.MethodGet, "/api/v1/lessons", ""},
	{"global lessons create", http.MethodPost, "/api/v1/lessons", `{}`},
	{"lessons update", http.MethodPatch, "/api/v1/lessons/nope", `{}`},
	{"lessons delete", http.MethodDelete, "/api/v1/lessons/nope", ""},
	{"schedules", http.MethodGet, "/api/v1/schedules", ""},
	{"intake rules", http.MethodGet, "/api/v1/intake-rules", ""},
	{"chat", http.MethodGet, "/api/v1/chat/sessions", ""},
	{"chat", http.MethodPost, "/api/v1/chat/sessions", `{}`},
	{"chat", http.MethodDelete, "/api/v1/chat/sessions/nope", ""},
	{"health", http.MethodGet, "/api/v1/health/providers", ""},
	{"health resume", http.MethodPost, "/api/v1/health/providers/claude/resume", `{}`},
	{"github auth status", http.MethodGet, "/api/v1/github/auth-status", ""},
	{"backup", http.MethodGet, "/api/v1/backup", ""},
	{"backup settings", http.MethodGet, "/api/v1/backup/settings", ""},
	{"backup settings", http.MethodPut, "/api/v1/backup/settings", `{}`},
	{"log retention settings", http.MethodGet, "/api/v1/log-retention/settings", ""},
	{"pricing settings", http.MethodGet, "/api/v1/settings/pricing", ""},
	{"cost-warning settings", http.MethodGet, "/api/v1/settings/cost-warning", ""},
	{"ws-ticket", http.MethodPost, "/api/v1/ws-ticket", `{}`},
	{"dashboard", http.MethodGet, "/api/v1/dashboard", ""},
	{"dashboard", http.MethodGet, "/api/v1/needs-me", ""},
}

func authSmokeDo(h http.Handler, method, path, body, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestRouterAuthSmoke_BearerGatedRoutes builds the real router (so middleware
// ordering is exercised, unlike handler-level tests) with both API_TOKEN and
// API_TOKENS configured and checks each group is gated: no/wrong token is 401,
// either valid token is not.
func TestRouterAuthSmoke_BearerGatedRoutes(t *testing.T) {
	r, _ := newWSTestRouter(t, "api-secret", map[string]string{"alice": "alice-secret"})

	for _, rt := range bearerGatedRoutes {
		t.Run(rt.group+" "+rt.method+" "+rt.path, func(t *testing.T) {
			for _, bad := range []string{"", "wrong-secret"} {
				if w := authSmokeDo(r, rt.method, rt.path, rt.body, bad); w.Code != http.StatusUnauthorized {
					t.Errorf("token %q: expected 401, got %d: %s", bad, w.Code, w.Body.String())
				}
			}
			for _, good := range []string{"api-secret", "alice-secret"} {
				if w := authSmokeDo(r, rt.method, rt.path, rt.body, good); w.Code == http.StatusUnauthorized {
					t.Errorf("token %q: expected non-401, got 401: %s", good, w.Body.String())
				}
			}
		})
	}
}

// TestRouterAuthSmoke_PublicRoutes covers the routes deliberately mounted
// outside BearerAuth: probes are open, and /ws plus the chat terminal route
// enforce their own single-use ?ticket= instead of a bearer header.
func TestRouterAuthSmoke_PublicRoutes(t *testing.T) {
	r, hub := newWSTestRouter(t, "api-secret", map[string]string{"alice": "alice-secret"})

	for _, path := range []string{"/healthz", "/readyz"} {
		if w := authSmokeDo(r, http.MethodGet, path, "", ""); w.Code == http.StatusUnauthorized {
			t.Errorf("%s: probe must not require a token, got 401", path)
		}
	}

	// /ws: a bearer header is not what authenticates it (tickets are), and a
	// missing ticket is rejected by the handler itself.
	if w := authSmokeDo(r, http.MethodGet, "/ws", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("/ws without ticket: expected 401, got %d", w.Code)
	}
	if w := authSmokeDo(r, http.MethodGet, "/ws", "", "api-secret"); w.Code != http.StatusUnauthorized {
		t.Errorf("/ws with bearer but no ticket: expected 401, got %d", w.Code)
	}

	// Chat terminal: ticket (not bearer) gets past auth; the unknown session
	// then 404s in the handler. Tickets are single-use.
	const term = "/api/v1/chat/sessions/nope/terminal"
	if w := authSmokeDo(r, http.MethodGet, term, "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("terminal without ticket: expected 401, got %d", w.Code)
	}
	if w := authSmokeDo(r, http.MethodGet, term, "", "api-secret"); w.Code != http.StatusUnauthorized {
		t.Errorf("terminal with bearer but no ticket: expected 401, got %d", w.Code)
	}
	ticket, err := hub.IssueTicket()
	if err != nil {
		t.Fatal(err)
	}
	if w := authSmokeDo(r, http.MethodGet, term+"?ticket="+ticket, "", ""); w.Code != http.StatusNotFound {
		t.Errorf("terminal with valid ticket: expected handler 404, got %d", w.Code)
	}
	if w := authSmokeDo(r, http.MethodGet, term+"?ticket="+ticket, "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("terminal with replayed ticket: expected 401, got %d", w.Code)
	}
}

// TestRouterAuthSmoke_GitHubWebhook checks POST /api/v1/github/webhook sits
// outside bearer auth (GitHub can't send our token) and is gated by the HMAC
// signature instead; with no secret configured the route is not usable.
func TestRouterAuthSmoke_GitHubWebhook(t *testing.T) {
	const secret = "whsec"
	body := `{"zen":"keep it logically awesome"}`
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	do := func(h http.Handler, signature string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/github/webhook", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", "ping")
		if signature != "" {
			req.Header.Set("X-Hub-Signature-256", signature)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	// API auth configured: the bearer token is neither required nor accepted
	// as a substitute for the signature.
	r := newWebhookTestRouter(t, "api-secret", secret)
	if w := do(r, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no signature: expected 401, got %d", w.Code)
	}
	if w := do(r, "sha256="+strings.Repeat("0", 64)); w.Code != http.StatusUnauthorized {
		t.Errorf("bad signature: expected 401, got %d", w.Code)
	}
	if w := do(r, sig); w.Code != http.StatusOK {
		t.Errorf("valid signature without bearer token: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Secret unset: route disabled even for a "valid" signature of an empty key.
	r = newWebhookTestRouter(t, "api-secret", "")
	emptyMac := hmac.New(sha256.New, nil)
	emptyMac.Write([]byte(body))
	if w := do(r, "sha256="+hex.EncodeToString(emptyMac.Sum(nil))); w.Code != http.StatusNotFound {
		t.Errorf("secret unset: expected 404, got %d", w.Code)
	}
}

type noopRefresher struct{}

func (noopRefresher) NoteWebhook()                                      {}
func (noopRefresher) RefreshMatching(context.Context, ghsync.PRRef) int { return 0 }

func newWebhookTestRouter(t *testing.T, apiToken, secret string) http.Handler {
	t.Helper()
	db := openRouterTestDB(t)
	hub := ws.NewHub()
	return api.NewRouter(api.RouterDeps{
		DB: db, Engine: workflow.New(db.SQL(), hub), Hub: hub, CORSOrigins: "*",
		BearerToken: apiToken, UploadDir: t.TempDir(),
		BackupInterval: 24 * time.Hour, BackupKeep: 7, Version: "dev", MaxWorkers: 5,
		GitHubWebhookSecret: secret, PRRefresher: noopRefresher{},
	})
}

// TestRouterAuthSmoke_Metrics checks /metrics' own token rules through the
// real router: METRICS_TOKEN replaces the API tokens, otherwise the API tokens
// gate it, and METRICS_PUBLIC opts out.
func TestRouterAuthSmoke_Metrics(t *testing.T) {
	named := map[string]string{"alice": "alice-secret"}
	cases := []struct {
		name          string
		metricsToken  string
		metricsPublic bool
		bearer        string
		want401       bool
	}{
		{"fallback: no token", "", false, "", true},
		{"fallback: api token", "", false, "api-secret", false},
		{"fallback: named token", "", false, "alice-secret", false},
		{"dedicated: api token rejected", "m-secret", false, "api-secret", true},
		{"dedicated: no token", "m-secret", false, "", true},
		{"dedicated: metrics token", "m-secret", false, "m-secret", false},
		{"public: no token", "", true, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouterFull(t, "api-secret", named, tc.metricsToken, tc.metricsPublic)
			w := authSmokeDo(r, http.MethodGet, "/metrics", "", tc.bearer)
			if got := w.Code == http.StatusUnauthorized; got != tc.want401 {
				t.Errorf("want 401=%v, got %d", tc.want401, w.Code)
			}
		})
	}
}
