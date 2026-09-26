package remotemcp

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/api"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

// TestToolsAgainstRealRouter drives tool calls through the real API router
// with API_TOKEN set, proving the in-process path gets past BearerAuth and the
// boardtools request shapes match the handlers.
func TestToolsAgainstRealRouter(t *testing.T) {
	f, err := os.CreateTemp("", "remotemcp-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.SeedDefaultWorkflow(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	hub := ws.NewHub()
	router := api.NewRouter(db, workflow.New(db.SQL(), hub), hub, "*", "static-api-token", nil, "", t.TempDir(), "", "", "", "", 24*time.Hour, 7, nil, nil, "", "dev", false, nil, 5, nil, nil)

	email := allowedEmail
	g := fakeGoogle(t, &email)
	t.Cleanup(g.Close)
	s, err := New(Config{
		PublicURL: "https://board.example.com/tasks", GoogleClientID: testClientID, GoogleClientSecret: "google-secret",
		AllowedEmails: []string{allowedEmail}, RedirectHosts: []string{"claude.ai"},
		GoogleAuthURL: "https://google.test/auth", GoogleTokenURL: g.URL,
	}, router)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Wrap(router)

	// The wrapped router still serves (and protects) the normal API.
	if rec := do(h, http.MethodGet, "/api/v1/tasks", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("API without token: %d", rec.Code)
	}

	clientID, _ := register(t, h, "https://claude.ai/api/mcp/auth_callback")
	back := authorize(t, h, clientID, pkce("v"))
	tok, status := token(h, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {back.Query().Get("code")}, "code_verifier": {"v"}})
	if status != http.StatusOK {
		t.Fatalf("token: %d %v", status, tok)
	}
	access := tok["access_token"].(string)

	call := func(name, args string) string {
		rec := rpc(h, access, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
		return rec.Body.String()
	}
	if out := call("list_workflows", `{}`); !strings.Contains(out, "Default") || !strings.Contains(out, `"isError":false`) {
		t.Errorf("list_workflows: %s", out)
	}
	if out := call("list_tasks", `{"label":"work"}`); !strings.Contains(out, `"text":"[]"`) {
		t.Errorf("list_tasks: %s", out)
	}
	if out := call("approve_task", `{"task_id":"missing"}`); !strings.Contains(out, "404") || !strings.Contains(out, `"isError":true`) {
		t.Errorf("approve_task on missing task: %s", out)
	}
}
