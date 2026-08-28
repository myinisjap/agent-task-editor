package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/api"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
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
	return api.NewRouter(db, engine, hub, "*", apiToken, nil, "", t.TempDir(), "", "", "", "", 24*time.Hour, 7, nil, nil, metricsToken, "dev", false, nil, 5, nil, nil)
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
