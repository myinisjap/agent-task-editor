package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
