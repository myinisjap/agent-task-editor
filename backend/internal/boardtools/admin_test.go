package boardtools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type rawSeen struct {
	method, path, contentType, body string
}

// rawAPI is stubAPI for tools that send non-JSON bodies: it records the raw
// body and Content-Type of each request.
func rawAPI(t *testing.T, routes map[string]string) (*Client, *[]rawSeen) {
	t.Helper()
	var calls []rawSeen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		calls = append(calls, rawSeen{r.Method, r.URL.RequestURI(), r.Header.Get("Content-Type"), string(data)})
		resp, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, HTTP: srv.Client()}, &calls
}

const wfJSON = `{"id":"w2","name":"Lean","labels":[{"name":"work"},{"name":"done"}],"transitions":[{},{}]}`

func TestWorkflowTools(t *testing.T) {
	c, calls := rawAPI(t, map[string]string{
		"GET /api/v1/workflows/w1/export.yaml": "name: Default\nlabels: []\n",
		"POST /api/v1/workflows/import":        wfJSON,
		"GET /api/v1/workflows/w2":             `{"id":"w2","labels":[{"name":"work"},{"name":"done"}]}`,
		"PUT /api/v1/workflows/w2/yaml":        wfJSON,
	})
	tools := Select(AdminRemoteTools)

	text, isErr := c.Call(tools, "get_workflow", json.RawMessage(`{"workflow_id":"w1"}`))
	if isErr || !strings.Contains(text, "name: Default") {
		t.Fatalf("get_workflow: %q", text)
	}

	doc := "name: Lean\nlabels:\n  - {name: work, color: \"#000000\", sort_order: 0}\n  - {name: done, color: \"#000000\", sort_order: 1}\n"
	args, _ := json.Marshal(map[string]string{"yaml": doc})
	text, isErr = c.Call(tools, "create_workflow", args)
	if isErr || !strings.Contains(text, `Created workflow "Lean" (w2) with labels [work, done] and 2 transitions`) {
		t.Fatalf("create_workflow: %q", text)
	}
	if last := (*calls)[len(*calls)-1]; last.contentType != "application/yaml" || last.body != doc {
		t.Errorf("create_workflow sent %+v", last)
	}

	args, _ = json.Marshal(map[string]string{"workflow_id": "w2", "yaml": doc})
	text, isErr = c.Call(tools, "update_workflow", args)
	if isErr || !strings.HasPrefix(text, "Updated workflow") {
		t.Fatalf("update_workflow: %q", text)
	}
	if last := (*calls)[len(*calls)-1]; last.method != http.MethodPut || last.path != "/api/v1/workflows/w2/yaml" || last.body != doc {
		t.Errorf("update_workflow sent %+v", last)
	}

	for _, tc := range []struct{ tool, args string }{
		{"get_workflow", `{}`},
		{"create_workflow", `{"yaml":"  "}`},
		{"update_workflow", `{"workflow_id":"w2"}`},
	} {
		if _, isErr := c.Call(tools, tc.tool, json.RawMessage(tc.args)); !isErr {
			t.Errorf("%s accepted %s", tc.tool, tc.args)
		}
	}
}

func TestUpdateWorkflowRefusesDroppingBusyLabel(t *testing.T) {
	c, calls := rawAPI(t, map[string]string{
		"GET /api/v1/workflows/w2": `{"id":"w2","labels":[{"name":"work"},{"name":"review"},{"name":"done"}]}`,
		// One task on "review" belongs to another workflow and doesn't count.
		"GET /api/v1/tasks": `[{"workflow_id":"w2"},{"workflow_id":"w2"},{"workflow_id":"other"}]`,
	})
	doc := "name: Lean\nlabels:\n  - {name: work}\n  - {name: done}\n"
	args, _ := json.Marshal(map[string]string{"workflow_id": "w2", "yaml": doc})
	text, isErr := c.Call(Select(AdminRemoteTools), "update_workflow", args)
	if !isErr || !strings.Contains(text, `"review" (2 tasks)`) {
		t.Fatalf("got %q (err=%v)", text, isErr)
	}
	for _, s := range *calls {
		if s.method == http.MethodPut {
			t.Fatal("workflow was replaced despite the refusal")
		}
		if s.method == http.MethodGet && strings.HasPrefix(s.path, "/api/v1/tasks?") &&
			(!strings.Contains(s.path, "label=review") || !strings.Contains(s.path, "archived=all")) {
			t.Errorf("task check query = %s", s.path)
		}
	}

	if text, isErr := c.Call(Select(AdminRemoteTools), "update_workflow", json.RawMessage(`{"workflow_id":"w2","yaml":"labels: ["}`)); !isErr || !strings.Contains(text, "invalid yaml") {
		t.Errorf("bad yaml: %q", text)
	}
}

func TestRepoTools(t *testing.T) {
	c, calls := stubAPI(t, map[string]string{
		"POST /api/v1/repos":                   `{"id":"r1","name":"acme/app","path":"/repos/acme/app","clone_status":"cloning"}`,
		"PATCH /api/v1/repos/r1":               `{"id":"r1","name":"acme/app","workflow_id":"w2","clone_status":"ready"}`,
		"GET /api/v1/repos/r1":                 `{"id":"r1","verify_command":"make test"}`,
		"POST /api/v1/repos/r1/runtime/detect": `{"suggestions":[{"id":"go","version":"1.26","source":"go.mod"}]}`,
		"GET /api/v1/agents":                   `[{"id":"a1","name":"Coder","labels":"work","enabled":true,"system_prompt":"long","provider_config":{"provider":"claude","model":"opus","env":"x"}}]`,
	})
	tools := Select(AdminRemoteTools)

	text, isErr := c.Call(tools, "add_repo", json.RawMessage(`{"remote_url":"https://github.com/acme/app","verify_command":"make test","bogus":1,"workflow_id":null}`))
	if isErr || !strings.Contains(text, "cloning in the background") || !strings.Contains(text, `"id":"r1"`) {
		t.Fatalf("add_repo: %q", text)
	}
	if b := (*calls)[0].body; b["remote_url"] != "https://github.com/acme/app" || b["verify_command"] != "make test" || len(b) != 2 {
		t.Errorf("add_repo body = %v", b)
	}

	text, isErr = c.Call(tools, "update_repo", json.RawMessage(`{"repo_id":"r1","workflow_id":"w2","path":"/etc","runtime_languages":[{"id":"go","version":"1.26"}]}`))
	if isErr || !strings.Contains(text, "Repo updated") {
		t.Fatalf("update_repo: %q", text)
	}
	if b := (*calls)[1].body; b["workflow_id"] != "w2" || b["path"] != nil || b["runtime_languages"] == nil || b["repo_id"] != nil {
		t.Errorf("update_repo body = %v", b)
	}

	for _, tc := range []struct{ tool, args, want string }{
		{"get_repo", `{"repo_id":"r1"}`, "make test"},
		{"detect_repo_runtime", `{"repo_id":"r1"}`, `"go.mod"`},
		{"list_agents", `{}`, `"provider":"claude"`},
	} {
		text, isErr := c.Call(tools, tc.tool, json.RawMessage(tc.args))
		if isErr || !strings.Contains(text, tc.want) {
			t.Errorf("%s: got %q (err=%v)", tc.tool, text, isErr)
		}
		if tc.tool == "list_agents" && (strings.Contains(text, "system_prompt") || strings.Contains(text, "env")) {
			t.Errorf("list_agents leaked config details: %q", text)
		}
	}

	for _, tc := range []struct{ tool, args string }{
		{"add_repo", `{"name":"x"}`},
		{"update_repo", `{"repo_id":"r1"}`},
		{"update_repo", `{"workflow_id":"w2"}`},
		{"get_repo", `{}`},
		{"detect_repo_runtime", `{}`},
	} {
		if _, isErr := c.Call(tools, tc.tool, json.RawMessage(tc.args)); !isErr {
			t.Errorf("%s accepted %s", tc.tool, tc.args)
		}
	}
}

func TestDestructiveHint(t *testing.T) {
	for _, tl := range Select(AdminRemoteTools) {
		ann := tl.Def()["annotations"].(map[string]any)
		if want := tl.Name == "update_workflow"; ann["destructiveHint"] != want {
			t.Errorf("%s destructiveHint = %v", tl.Name, ann["destructiveHint"])
		}
	}
}
