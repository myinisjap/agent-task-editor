package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
)

func TestCORS_Wildcard_SetsLiteralStar(t *testing.T) {
	h := middleware.CORS("*")(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	// Wildcard must be the literal "*", not the reflected origin, so
	// browsers enforce same-origin cookie policy (no Allow-Credentials).
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected ACAO=*, got %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("expected no Allow-Credentials with wildcard, got %q", got)
	}
}

func TestCORS_SpecificOrigin_Allowed(t *testing.T) {
	h := middleware.CORS("https://app.example.com")(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("expected ACAO header set for allowed origin, got %q", got)
	}
}

func TestCORS_SpecificOrigin_Blocked(t *testing.T) {
	h := middleware.CORS("https://app.example.com")(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Handler still runs (CORS doesn't block the request), just no ACAO header
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no ACAO header for disallowed origin, got %q", got)
	}
}

func TestCORS_MultipleOrigins(t *testing.T) {
	h := middleware.CORS("https://a.com, https://b.com")(http.HandlerFunc(okHandler))

	for _, origin := range []string{"https://a.com", "https://b.com"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("origin %q: expected ACAO=%q, got %q", origin, origin, got)
		}
	}
}

func TestCORS_Preflight_Returns204(t *testing.T) {
	h := middleware.CORS("*")(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/tasks", nil)
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS preflight, got %d", w.Code)
	}
}

func TestCORS_MethodsHeaderAlwaysSet(t *testing.T) {
	h := middleware.CORS("*")(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("expected Access-Control-Allow-Methods header always present")
	}
}
