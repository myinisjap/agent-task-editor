package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent/runtime"
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

// TestChatTerminal_KnownSession_ProvisionFailure_ReturnsInternalError drives
// Terminal past the auth gate and the session/repo/provider-config lookups
// (all real DB rows) into the worktree provisioning step. The fixture repo's
// path is a plain temp dir (no .git), so agent.ProvisionChatWorktree fails
// deterministically and hermetically — no fake seam needed, just local git
// against a non-repo directory — exercising the "provision worktree failed"
// 500 branch that was previously only reachable via a real git repo.
func TestChatTerminal_KnownSession_ProvisionFailure_ReturnsInternalError(t *testing.T) {
	router, q, _ := setupChatRouter(t, "", &fakeTerminal{})
	repoID, providerConfigID := seedChatFixtures(t, q)

	sessionID := uuid.NewString()
	if _, err := q.CreateChatSession(context.Background(), gen.CreateChatSessionParams{
		ID:               sessionID,
		RepoID:           repoID,
		ProviderConfigID: providerConfigID,
		Title:            "provision failure",
	}); err != nil {
		t.Fatalf("create chat session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/chat/"+sessionID+"/terminal", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when worktree provisioning fails, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "provision worktree failed") {
		t.Errorf("expected provision-worktree error message, got: %s", w.Body.String())
	}
}

// fakeTerminal is a no-op handlers.Terminal implementation for tests that
// only need to exercise the auth gate ahead of it, never real PTY attach.
type fakeTerminal struct{}

func (f *fakeTerminal) Attach(ctx context.Context, sessionID, repoPath, provider, model string, resume bool, repoID string, pins []runtime.Pin, conn *websocket.Conn) error {
	return nil
}

func (f *fakeTerminal) Stop(sessionID string) {}

// ---------- List / Create / Get / Delete ----------

// seedChatFixtures creates a repo and provider config for chat session tests.
func seedChatFixtures(t *testing.T, q *gen.Queries) (repoID, providerConfigID string) {
	t.Helper()
	repoID = uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:   repoID,
		Name: "chat-repo",
		Path: t.TempDir(),
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	providerConfigID = uuid.NewString()
	if _, err := q.CreateProviderConfig(context.Background(), gen.CreateProviderConfigParams{
		ID:       providerConfigID,
		Name:     "test-provider",
		Provider: "claude",
		Model:    "sonnet",
		Env:      "{}",
	}); err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	return repoID, providerConfigID
}

func TestChat_Create_OK(t *testing.T) {
	router, q, _ := setupChatRouter(t, "", nil)
	repoID, providerConfigID := seedChatFixtures(t, q)

	body := map[string]string{"repo_id": repoID, "provider_config_id": providerConfigID, "title": "My chat"}
	req := httptest.NewRequest(http.MethodPost, "/chat", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body)
	}
}

func TestChat_Create_MissingRepoID(t *testing.T) {
	router, q, _ := setupChatRouter(t, "", nil)
	_, providerConfigID := seedChatFixtures(t, q)

	body := map[string]string{"provider_config_id": providerConfigID}
	req := httptest.NewRequest(http.MethodPost, "/chat", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestChat_Create_MissingProviderConfigID(t *testing.T) {
	router, q, _ := setupChatRouter(t, "", nil)
	repoID, _ := seedChatFixtures(t, q)

	body := map[string]string{"repo_id": repoID}
	req := httptest.NewRequest(http.MethodPost, "/chat", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestChat_Create_UnknownRepo(t *testing.T) {
	router, q, _ := setupChatRouter(t, "", nil)
	_, providerConfigID := seedChatFixtures(t, q)

	body := map[string]string{"repo_id": uuid.NewString(), "provider_config_id": providerConfigID}
	req := httptest.NewRequest(http.MethodPost, "/chat", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestChat_Create_UnknownProviderConfig(t *testing.T) {
	router, q, _ := setupChatRouter(t, "", nil)
	repoID, _ := seedChatFixtures(t, q)

	body := map[string]string{"repo_id": repoID, "provider_config_id": uuid.NewString()}
	req := httptest.NewRequest(http.MethodPost, "/chat", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestChat_Create_InvalidBody(t *testing.T) {
	router, _, _ := setupChatRouter(t, "", nil)

	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestChat_List_ReturnsCreatedSessions(t *testing.T) {
	router, q, _ := setupChatRouter(t, "", nil)
	repoID, providerConfigID := seedChatFixtures(t, q)

	body := map[string]string{"repo_id": repoID, "provider_config_id": providerConfigID, "title": "chat 1"}
	req := httptest.NewRequest(http.MethodPost, "/chat", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body)
	}

	req = httptest.NewRequest(http.MethodGet, "/chat", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", w.Code, w.Body)
	}
	var sessions []gen.ChatSession
	if err := json.Unmarshal(w.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Title != "chat 1" {
		t.Errorf("expected 1 session titled 'chat 1', got %+v", sessions)
	}
}

func TestChat_Get_OK(t *testing.T) {
	router, q, _ := setupChatRouter(t, "", nil)
	repoID, providerConfigID := seedChatFixtures(t, q)
	sess, err := q.CreateChatSession(context.Background(), gen.CreateChatSessionParams{
		ID: uuid.NewString(), RepoID: repoID, ProviderConfigID: providerConfigID, Title: "chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/chat/"+sess.ID, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var got struct {
		Session        gen.ChatSession     `json:"session"`
		ProviderConfig *gen.ProviderConfig `json:"provider_config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Session.ID != sess.ID {
		t.Errorf("expected session id %s, got %s", sess.ID, got.Session.ID)
	}
	if got.ProviderConfig == nil || got.ProviderConfig.ID != providerConfigID {
		t.Errorf("expected provider config %s, got %+v", providerConfigID, got.ProviderConfig)
	}
}

func TestChat_Get_Unknown(t *testing.T) {
	router, _, _ := setupChatRouter(t, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/chat/"+uuid.NewString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestChat_Delete_OK(t *testing.T) {
	router, q, _ := setupChatRouter(t, "", nil)
	repoID, providerConfigID := seedChatFixtures(t, q)
	sess, err := q.CreateChatSession(context.Background(), gen.CreateChatSessionParams{
		ID: uuid.NewString(), RepoID: repoID, ProviderConfigID: providerConfigID, Title: "chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/chat/"+sess.ID, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body)
	}
	if _, err := q.GetChatSession(context.Background(), sess.ID); err == nil {
		t.Errorf("expected session to be deleted")
	}
}

func TestChat_Delete_Unknown(t *testing.T) {
	router, _, _ := setupChatRouter(t, "", nil)

	req := httptest.NewRequest(http.MethodDelete, "/chat/"+uuid.NewString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
