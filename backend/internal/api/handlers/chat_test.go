package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"nhooyr.io/websocket"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

func setupChatRouter(t *testing.T, bearerToken string, term handlers.Terminal) (http.Handler, *gen.Queries, *ws.Hub) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	hub := ws.NewHub()
	h := handlers.NewChatHandler(q, hub, term, bearerToken, "")

	r := chi.NewRouter()
	r.Get("/chat", h.List)
	r.Post("/chat", h.Create)
	r.Get("/chat/{id}", h.Get)
	r.Delete("/chat/{id}", h.Delete)
	r.Get("/chat/{id}/terminal", h.Terminal)
	return r, q, hub
}

// TestChatTerminal_MissingTicket_RejectedWhenAuthConfigured is the chat/PTY
// analog of middleware/auth_test.go's
// TestBearerAuth_WebSocketUpgrade_DoesNotBypass: the Terminal WS upgrade is a
// second WS auth surface (alongside ServeWS) that previously had zero test
// coverage. A request with no ?ticket= at all must be rejected with 401 when
// a bearer token is configured, before ever touching chi URL params/DB
// lookups (single-use ticket, see ChatHandler.Terminal / ws/ticket.go).
func TestChatTerminal_MissingTicket_RejectedWhenAuthConfigured(t *testing.T) {
	router, _, _ := setupChatRouter(t, "s3cr3t", &fakeTerminal{})

	req := httptest.NewRequest(http.MethodGet, "/chat/does-not-matter/terminal", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for a terminal upgrade with no ticket, got %d: %s", w.Code, w.Body.String())
	}
}

// TestChatTerminal_InvalidTicket_Rejected verifies a well-formed but unknown/
// expired ticket is rejected the same way as a missing one.
func TestChatTerminal_InvalidTicket_Rejected(t *testing.T) {
	router, _, _ := setupChatRouter(t, "s3cr3t", &fakeTerminal{})

	req := httptest.NewRequest(http.MethodGet, "/chat/does-not-matter/terminal?ticket=bogus-ticket", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for an invalid ticket, got %d: %s", w.Code, w.Body.String())
	}
}

// TestChatTerminal_ValidTicket_ProceedsPastAuth verifies that a genuine
// single-use ticket minted by the hub passes the auth gate (the request then
// 404s on the unknown chat session id, proving auth was NOT what stopped
// it — this isolates the auth check from the rest of Terminal's logic).
func TestChatTerminal_ValidTicket_ProceedsPastAuth(t *testing.T) {
	router, _, hub := setupChatRouter(t, "s3cr3t", &fakeTerminal{})

	ticket, err := hub.IssueTicket()
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/chat/unknown-session-id/terminal?ticket="+ticket, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Fatalf("valid ticket should not be rejected as unauthorized, got 401: %s", w.Body.String())
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for an unknown chat session past auth, got %d: %s", w.Code, w.Body.String())
	}
}

// TestChatTerminal_NoBearerTokenConfigured_OpenAuth verifies that when no
// bearer token is configured (open/no-auth mode, matching ServeWS's
// behavior), a terminal upgrade with no ticket is NOT rejected for auth
// reasons — it proceeds to the (still 404, unknown session) rest of the
// handler.
func TestChatTerminal_NoBearerTokenConfigured_OpenAuth(t *testing.T) {
	router, _, _ := setupChatRouter(t, "", &fakeTerminal{})

	req := httptest.NewRequest(http.MethodGet, "/chat/unknown-session-id/terminal", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Fatalf("open auth (no bearer token configured) should not 401, got: %s", w.Body.String())
	}
}

// TestChatTerminal_NilTerminalManager_ServiceUnavailable verifies the
// feature-unavailable branch is checked before auth would even matter in
// practice — a nil Terminal manager always reports 503 regardless of ticket.
func TestChatTerminal_NilTerminalManager_ServiceUnavailable(t *testing.T) {
	router, _, _ := setupChatRouter(t, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/chat/does-not-matter/terminal", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when no Terminal manager is wired, got %d: %s", w.Code, w.Body.String())
	}
}

// fakeTerminal is a no-op handlers.Terminal implementation for tests that
// only need to exercise the auth gate ahead of it, never real PTY attach.
type fakeTerminal struct{}

func (f *fakeTerminal) Attach(ctx context.Context, sessionID, repoPath, provider, model string, resume bool, conn *websocket.Conn) error {
	return nil
}

func (f *fakeTerminal) Stop(sessionID string) {}
