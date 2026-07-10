package api_test

import (
	"context"
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
	return api.NewRouter(db, engine, hub, "*", apiToken, "", t.TempDir(), "", "", "", "", 24*time.Hour, 7, nil, nil, metricsToken)
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
