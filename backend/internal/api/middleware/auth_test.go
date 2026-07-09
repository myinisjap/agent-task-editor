package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
)

func okHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func TestBearerAuth_EmptyToken_IsNoOp(t *testing.T) {
	h := middleware.BearerAuth("", nil)(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 when token is empty, got %d", w.Code)
	}
}

func TestBearerAuth_ValidToken(t *testing.T) {
	h := middleware.BearerAuth("s3cr3t", nil)(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer s3cr3t")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestBearerAuth_WrongToken_Rejects(t *testing.T) {
	h := middleware.BearerAuth("s3cr3t", nil)(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestBearerAuth_NoHeader_Rejects(t *testing.T) {
	h := middleware.BearerAuth("s3cr3t", nil)(http.HandlerFunc(okHandler))

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

// TestBearerAuth_WebSocketUpgrade_DoesNotBypass is a regression test for a
// security bug where any request carrying `Upgrade: websocket` skipped bearer
// validation on every API route (not just /ws). The bypass has been removed;
// the /ws route is now mounted outside this middleware instead, and it does its
// own ?token= check. A tokenless upgrade request to a protected route must 401.
func TestBearerAuth_WebSocketUpgrade_DoesNotBypass(t *testing.T) {
	h := middleware.BearerAuth("s3cr3t", nil)(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for WS upgrade without token, got %d", w.Code)
	}
}

func TestBearerAuth_TokenCaseSensitive(t *testing.T) {
	h := middleware.BearerAuth("MyToken", nil)(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer mytoken")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong-case token, got %d", w.Code)
	}
}

func TestBearerAuth_NamedToken_ResolvesActor(t *testing.T) {
	var gotActor string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotActor = middleware.ActorFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.BearerAuth("", map[string]string{"alice": "tok-alice", "bob": "tok-bob"})(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer tok-alice")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if gotActor != "alice" {
		t.Errorf("expected actor 'alice', got %q", gotActor)
	}
}

func TestBearerAuth_NamedToken_OtherName(t *testing.T) {
	var gotActor string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotActor = middleware.ActorFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.BearerAuth("", map[string]string{"alice": "tok-alice", "bob": "tok-bob"})(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer tok-bob")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if gotActor != "bob" {
		t.Errorf("expected actor 'bob', got %q", gotActor)
	}
}

func TestBearerAuth_LegacyToken_FallsBackToAnonymousActor(t *testing.T) {
	var gotActor string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotActor = middleware.ActorFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.BearerAuth("legacy-tok", map[string]string{"alice": "tok-alice"})(handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer legacy-tok")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if gotActor != "" {
		t.Errorf("expected empty (anonymous) actor for legacy token, got %q", gotActor)
	}
}

func TestBearerAuth_UnknownToken_Rejects(t *testing.T) {
	h := middleware.BearerAuth("legacy-tok", map[string]string{"alice": "tok-alice"})(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer some-other-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unknown token, got %d", w.Code)
	}
}

func TestBearerAuth_NamedTokensOnly_NoLegacyToken_IsNotNoOp(t *testing.T) {
	h := middleware.BearerAuth("", map[string]string{"alice": "tok-alice"})(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with no auth header when named tokens are configured, got %d", w.Code)
	}
}

func TestBearerAuth_NoTokensAtAll_IsNoOp(t *testing.T) {
	h := middleware.BearerAuth("", nil)(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 when both bearerToken and namedTokens are empty, got %d", w.Code)
	}
}

func TestActorFromContext_NoValue_ReturnsEmpty(t *testing.T) {
	if got := middleware.ActorFromContext(context.Background()); got != "" {
		t.Errorf("expected empty string for context with no actor set, got %q", got)
	}
}
