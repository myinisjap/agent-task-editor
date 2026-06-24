package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
)

func okHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func TestBearerAuth_EmptyToken_IsNoOp(t *testing.T) {
	h := middleware.BearerAuth("")(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 when token is empty, got %d", w.Code)
	}
}

func TestBearerAuth_ValidToken(t *testing.T) {
	h := middleware.BearerAuth("s3cr3t")(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer s3cr3t")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestBearerAuth_WrongToken_Rejects(t *testing.T) {
	h := middleware.BearerAuth("s3cr3t")(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestBearerAuth_NoHeader_Rejects(t *testing.T) {
	h := middleware.BearerAuth("s3cr3t")(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no auth header, got %d", w.Code)
	}
	if www := w.Header().Get("WWW-Authenticate"); www == "" {
		t.Error("expected WWW-Authenticate header in 401 response")
	}
}

func TestBearerAuth_WebSocketUpgrade_Bypasses(t *testing.T) {
	h := middleware.BearerAuth("s3cr3t")(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for WS upgrade without token, got %d", w.Code)
	}
}

func TestBearerAuth_TokenCaseInsensitive(t *testing.T) {
	h := middleware.BearerAuth("MyToken")(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer mytoken")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for case-insensitive token match, got %d", w.Code)
	}
}
