package metrics_test

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
)

func TestHandler_ServesPrometheusFormat(t *testing.T) {
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	metrics.Handler().ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close() //nolint:errcheck

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("expected Content-Type to start with text/plain, got %q", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// Standard Go process collector metric — proves the default collectors
	// registered on package init.
	if !strings.Contains(string(body), "go_goroutines") {
		t.Errorf("expected body to contain go_goroutines metric, got:\n%s", body)
	}

	// A custom metric — proves the custom collectors registered on package init.
	if !strings.Contains(string(body), "ate_pool_max_workers") {
		t.Errorf("expected body to contain ate_pool_max_workers metric, got:\n%s", body)
	}
}
