package remotemcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	router := api.NewRouter(api.RouterDeps{
		DB: db, Engine: workflow.New(db.SQL(), hub), Hub: hub,
		CORSOrigins: "*", BearerToken: "static-api-token",
		UploadDir: t.TempDir(), BackupInterval: 24 * time.Hour, BackupKeep: 7,
		Version: "dev", MaxWorkers: 5,
	})

	email := allowedEmail
	g := fakeGoogle(t, &email)
	t.Cleanup(g.Close)
	s, err := New(func() Config {
		c := testConfig(t, g.URL)
		c.PublicURL = "https://board.example.com/tasks"
		c.DB = db.SQL()
		return c
	}(), router)
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
	if out := call("get_costs", `{}`); !strings.Contains(out, "Total") || !strings.Contains(out, `"isError":false`) {
		t.Errorf("get_costs: %s", out)
	}
	if out := call("create_pr", `{"task_id":"missing"}`); !strings.Contains(out, "create_pr failed") || !strings.Contains(out, `"isError":true`) {
		t.Errorf("create_pr on missing task: %s", out)
	}
	if out := call("create_workflow", `{"yaml":"name: X"}`); !strings.Contains(out, "unknown tool") {
		t.Errorf("full profile ran create_workflow: %s", out)
	}
	if out := call("list_needs_me", `{}`); !strings.Contains(out, `"text":"[]"`) || !strings.Contains(out, `"isError":false`) {
		t.Errorf("list_needs_me: %s", out)
	}
	if out := call("rerun_task", `{"task_id":"missing"}`); !strings.Contains(out, "404") || !strings.Contains(out, `"isError":true`) {
		t.Errorf("rerun_task on missing task: %s", out)
	}
	if out := call("cancel_run", `{"task_id":"missing","run_id":"r"}`); !strings.Contains(out, "404") || !strings.Contains(out, `"isError":true`) {
		t.Errorf("cancel_run on missing task: %s", out)
	}
}

// TestAdminToolsAgainstRealRouter checks the admin tools' request shapes
// (YAML bodies included) against the real handlers and middleware.
func TestAdminToolsAgainstRealRouter(t *testing.T) {
	f, err := os.CreateTemp("", "remotemcp-admin-*.db")
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
	hub := ws.NewHub()
	router := api.NewRouter(api.RouterDeps{
		DB: db, Engine: workflow.New(db.SQL(), hub), Hub: hub,
		CORSOrigins: "*", BearerToken: "static-api-token",
		UploadDir: t.TempDir(), BackupInterval: 24 * time.Hour, BackupKeep: 7,
		Version: "dev", MaxWorkers: 5,
	})

	email := allowedEmail
	g := fakeGoogle(t, &email)
	t.Cleanup(g.Close)
	s, err := New(func() Config {
		c := testConfig(t, g.URL)
		c.DB = db.SQL()
		c.ToolProfile = ToolProfileAdmin
		return c
	}(), router)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Wrap(router)
	clientID, _ := register(t, h, "https://claude.ai/api/mcp/auth_callback")
	back := authorize(t, h, clientID, pkce("v"))
	tok, status := token(h, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {back.Query().Get("code")}, "code_verifier": {"v"}})
	if status != http.StatusOK {
		t.Fatalf("token: %d %v", status, tok)
	}
	access := tok["access_token"].(string)

	// result runs a tool and returns its text, failing on a tool error.
	result := func(name string, args any) string {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		rec := rpc(h, access, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+string(params)+`}`)
		var resp struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Result.Content) == 0 {
			t.Fatalf("%s: %s", name, rec.Body.String())
		}
		if resp.Result.IsError {
			t.Fatalf("%s failed: %s", name, resp.Result.Content[0].Text)
		}
		return resp.Result.Content[0].Text
	}

	doc := `name: Lean
labels:
  - {name: todo, color: "#6B7280", sort_order: 0, agent_ignore: true}
  - {name: work, color: "#3B82F6", sort_order: 1}
  - {name: review, color: "#EC4899", sort_order: 2}
transitions:
  - {from: todo, to: work, trigger: human}
  - {from: work, to: review, trigger: agent, path: success}
`
	out := result("create_workflow", map[string]any{"yaml": doc})
	m := regexp.MustCompile(`\(([0-9a-f-]{36})\)`).FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, "[todo, work, review]") {
		t.Fatalf("create_workflow: %s", out)
	}
	wfID := m[1]

	exported := result("get_workflow", map[string]any{"workflow_id": wfID})
	if !strings.Contains(exported, "name: Lean") || !strings.Contains(exported, "trigger: agent") {
		t.Fatalf("get_workflow: %s", exported)
	}

	edited := strings.Replace(doc, "name: Lean", "name: Leaner", 1) + "  - {from: review, to: work, trigger: human, path: failure}\n"
	if out := result("update_workflow", map[string]any{"workflow_id": wfID, "yaml": edited}); !strings.Contains(out, `"Leaner"`) || !strings.Contains(out, "3 transitions") {
		t.Fatalf("update_workflow: %s", out)
	}

	// Onboard a local checkout onto the new workflow.
	repoDir := t.TempDir()
	if err := exec.Command("git", "init", "-q", repoDir).Run(); err != nil {
		t.Skip("git unavailable:", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module x\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = result("add_repo", map[string]any{"name": "local", "path": repoDir, "workflow_id": wfID})
	m = regexp.MustCompile(`"id":"([0-9a-f-]{36})"`).FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, `"workflow_id":"`+wfID+`"`) {
		t.Fatalf("add_repo: %s", out)
	}
	repoID := m[1]
	if out := result("detect_repo_runtime", map[string]any{"repo_id": repoID}); !strings.Contains(out, `"go"`) {
		t.Errorf("detect_repo_runtime: %s", out)
	}
	result("update_repo", map[string]any{"repo_id": repoID, "verify_command": "go test ./...", "runtime_languages": []map[string]string{{"id": "go", "version": "1.26"}}})
	if out := result("get_repo", map[string]any{"repo_id": repoID}); !strings.Contains(out, `"verify_command":"go test ./..."`) || !strings.Contains(out, `1.26`) {
		t.Errorf("get_repo: %s", out)
	}

	// Recurring work: a template and a schedule, then pause the schedule.
	out = result("create_template", map[string]any{"name": "Triage", "title": "Triage open issues", "type": "chore"})
	m = regexp.MustCompile(`"id":"([0-9a-f-]{36})"`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("create_template: %s", out)
	}
	tplID := m[1]
	if out := result("update_template", map[string]any{"template_id": tplID, "description": "Look at each one"}); !strings.Contains(out, `"title":"Triage open issues"`) || !strings.Contains(out, "Look at each one") {
		t.Errorf("update_template: %s", out)
	}
	out = result("create_schedule", map[string]any{"template_id": tplID, "repo_id": repoID, "cron_expr": "0 9 * * 1", "target_label": "todo"})
	m = regexp.MustCompile(`"id":"([0-9a-f-]{36})"`).FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, `"enabled":true`) {
		t.Fatalf("create_schedule: %s", out)
	}
	schedID := m[1]
	if out := result("update_schedule", map[string]any{"schedule_id": schedID, "enabled": false}); !strings.Contains(out, `"enabled":false`) || !strings.Contains(out, `"cron_expr":"0 9 * * 1"`) || !strings.Contains(out, `"target_label":"todo"`) {
		t.Errorf("update_schedule pause: %s", out)
	}
	if out := result("update_schedule", map[string]any{"schedule_id": schedID, "cron_expr": "0 8 * * *"}); !strings.Contains(out, `"enabled":false`) || !strings.Contains(out, `"0 8 * * *"`) {
		t.Errorf("update_schedule cron: %s", out)
	}
	if out := result("list_templates", map[string]any{}); !strings.Contains(out, "Triage") {
		t.Errorf("list_templates: %s", out)
	}
	if out := result("list_schedules", map[string]any{}); !strings.Contains(out, schedID) {
		t.Errorf("list_schedules: %s", out)
	}

	// Wire an agent to the new workflow's label.
	var pcID string
	{
		rec := do(h, http.MethodPost, "/api/v1/provider-configs", "application/json", `{"name":"P","provider":"claude","model":"opus","env":"{\"SECRET\":\"hunter2\"}"}`, "Authorization", "Bearer static-api-token")
		var pc struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &pc); err != nil || pc.ID == "" {
			t.Fatalf("create provider config: %d %s", rec.Code, rec.Body.String())
		}
		pcID = pc.ID
	}
	if out := result("list_provider_configs", map[string]any{}); !strings.Contains(out, pcID) || strings.Contains(out, "hunter2") || strings.Contains(out, "env") {
		t.Errorf("list_provider_configs: %s", out)
	}
	out = result("create_agent", map[string]any{"name": "Coder", "provider_config_id": pcID, "system_prompt": "ship it", "labels": []string{"work"}, "max_turns": 20, "priority": 1})
	m = regexp.MustCompile(`"id":"([0-9a-f-]{36})"`).FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, `"enabled":true`) || strings.Contains(out, "hunter2") {
		t.Fatalf("create_agent: %s", out)
	}
	agentID := m[1]
	out = result("update_agent", map[string]any{"agent_id": agentID, "labels": []string{"work", "review"}, "enabled": false})
	if !strings.Contains(out, `"enabled":false`) || strings.Contains(out, "hunter2") {
		t.Fatalf("update_agent: %s", out)
	}
	out = result("get_agent", map[string]any{"agent_id": agentID})
	for _, want := range []string{`"system_prompt":"ship it"`, `\"work\",\"review\"`, `"max_turns":20`, `"priority":1`, `"timeout_secs":600`, `"provider":"claude"`} {
		if !strings.Contains(out, want) {
			t.Errorf("get_agent missing %s: %s", want, out)
		}
	}
	if strings.Contains(out, "hunter2") || strings.Contains(out, `"env"`) {
		t.Errorf("get_agent leaked env: %s", out)
	}

	// A task on "review" blocks dropping that label.
	if out := result("create_task", map[string]any{"title": "t", "repo_id": repoID, "workflow_id": wfID, "label": "review"}); !strings.Contains(out, "review") {
		t.Fatalf("create_task: %s", out)
	}
	params, _ := json.Marshal(map[string]any{"name": "update_workflow", "arguments": map[string]any{"workflow_id": wfID, "yaml": "name: Leaner\nlabels:\n  - {name: work, color: \"#3B82F6\", sort_order: 0}\n"}})
	if body := rpc(h, access, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+string(params)+`}`).Body.String(); !strings.Contains(body, "still hold tasks") {
		t.Errorf("update_workflow dropped a busy label: %s", body)
	}
}

// TestToolsAgainstUnauthenticatedRouter is TestToolsAgainstRealRouter without
// an API_TOKEN. That turns on the API's DNS-rebinding Host check, which
// rejects an empty Host — so a path-only in-process request URL fails every
// tool call with a 403 ("Host is not allowed while the API is
// unauthenticated"). Regression test for that: tool calls must work whether or
// not the deployment sets a token.
func TestToolsAgainstUnauthenticatedRouter(t *testing.T) {
	f, err := os.CreateTemp("", "remotemcp-noauth-*.db")
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
	// No BearerToken: the Host policy is enforced in this mode.
	router := api.NewRouter(api.RouterDeps{
		DB: db, Engine: workflow.New(db.SQL(), hub), Hub: hub,
		UploadDir: t.TempDir(), BackupInterval: 24 * time.Hour, BackupKeep: 7,
		Version: "dev", MaxWorkers: 5,
	})

	email := allowedEmail
	g := fakeGoogle(t, &email)
	t.Cleanup(g.Close)
	s, err := New(func() Config {
		c := testConfig(t, g.URL)
		c.PublicURL = "https://board.example.com/tasks"
		c.DB = db.SQL()
		return c
	}(), router)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Wrap(router)

	clientID, _ := register(t, h, "https://claude.ai/api/mcp/auth_callback")
	back := authorize(t, h, clientID, pkce("v"))
	tok, status := token(h, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {back.Query().Get("code")}, "code_verifier": {"v"}})
	if status != http.StatusOK {
		t.Fatalf("token: %d %v", status, tok)
	}
	access := tok["access_token"].(string)

	out := rpc(h, access, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_workflows","arguments":{}}}`).Body.String()
	if strings.Contains(out, "DNS-rebinding") {
		t.Fatalf("in-process call blocked by Host check: %s", out)
	}
	if !strings.Contains(out, "Default") || !strings.Contains(out, `"isError":false`) {
		t.Errorf("list_workflows: %s", out)
	}
}
