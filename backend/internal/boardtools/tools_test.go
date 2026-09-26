package boardtools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type seen struct {
	method, path string
	body         map[string]any
}

// stubAPI records each request and answers from routes (keyed "METHOD path").
func stubAPI(t *testing.T, routes map[string]string) (*Client, *[]seen) {
	t.Helper()
	var calls []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := seen{method: r.Method, path: r.URL.RequestURI()}
		if data, _ := io.ReadAll(r.Body); len(data) > 0 {
			_ = json.Unmarshal(data, &s.body)
		}
		calls = append(calls, s)
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

func TestReplyToRun_DefaultsToActiveRun(t *testing.T) {
	c, calls := stubAPI(t, map[string]string{
		"GET /api/v1/tasks/t1":                `{"active_agent_run_id":"r9"}`,
		"POST /api/v1/tasks/t1/runs/r9/reply": `{"run_id":"r10"}`,
	})
	text, isErr := c.Call(Select(RemoteTools), "reply_to_run", json.RawMessage(`{"task_id":"t1","message":"use postgres"}`))
	if isErr || !strings.Contains(text, "r10") {
		t.Fatalf("got %q (err=%v)", text, isErr)
	}
	last := (*calls)[len(*calls)-1]
	if last.body["message"] != "use postgres" {
		t.Errorf("reply body = %v", last.body)
	}
}

func TestTransitions(t *testing.T) {
	c, calls := stubAPI(t, map[string]string{
		"POST /api/v1/tasks/t1/approve": `{"label":"done"}`,
		"POST /api/v1/tasks/t1/reject":  `{"label":"work"}`,
		"PATCH /api/v1/tasks/t1/label":  `{"label":"not_ready"}`,
	})
	tools := Select(RemoteTools)
	for _, tc := range []struct {
		tool, args, wantLabel, wantPath string
	}{
		{"approve_task", `{"task_id":"t1","note":"lgtm"}`, "done", "/api/v1/tasks/t1/approve"},
		{"reject_task", `{"task_id":"t1","note":"add tests","to_label":"work"}`, "work", "/api/v1/tasks/t1/reject"},
		{"move_task", `{"task_id":"t1","to_label":"not_ready"}`, "not_ready", "/api/v1/tasks/t1/label"},
	} {
		text, isErr := c.Call(tools, tc.tool, json.RawMessage(tc.args))
		if isErr || !strings.Contains(text, tc.wantLabel) {
			t.Errorf("%s: got %q (err=%v)", tc.tool, text, isErr)
		}
		if last := (*calls)[len(*calls)-1]; last.path != tc.wantPath {
			t.Errorf("%s: hit %s", tc.tool, last.path)
		}
	}
	if (*calls)[1].body["to_label"] != "work" || (*calls)[0].body["note"] != "lgtm" {
		t.Errorf("bodies: %+v", *calls)
	}
}

func TestListTasks_PassesFilters(t *testing.T) {
	c, calls := stubAPI(t, map[string]string{"GET /api/v1/tasks": `[{"id":"t1","title":"x","label":"review","description":"long"}]`})
	text, isErr := c.Call(Select(RemoteTools), "list_tasks", json.RawMessage(`{"label":"review","limit":999}`))
	if isErr || strings.Contains(text, "description") {
		t.Fatalf("got %q", text)
	}
	if p := (*calls)[0].path; !strings.Contains(p, "label=review") || !strings.Contains(p, "limit=200") {
		t.Errorf("query = %s", p)
	}
}

func TestStdioToolsExcludeRemoteOnly(t *testing.T) {
	c := &Client{}
	if text, isErr := c.Call(Select(StdioTools), "approve_task", nil); !isErr || !strings.Contains(text, "unknown tool") {
		t.Errorf("stdio set exposed approve_task: %q", text)
	}
}

func TestBackendErrorSurfaces(t *testing.T) {
	c, _ := stubAPI(t, map[string]string{})
	text, isErr := c.Call(Select(RemoteTools), "move_task", json.RawMessage(`{"task_id":"t1","to_label":"x"}`))
	if !isErr || !strings.Contains(text, "404") || !strings.Contains(text, "not found") {
		t.Errorf("got %q", text)
	}
}

func TestReadTools(t *testing.T) {
	c, calls := stubAPI(t, map[string]string{
		"GET /api/v1/repos":                 `[{"id":"r1","name":"acme","workflow_id":"w1","clone_status":"ready"}]`,
		"GET /api/v1/workflows":             `[{"id":"w1","name":"Default","labels":[{"name":"work"},{"name":"review"}]}]`,
		"GET /api/v1/tasks/t1":              `{"id":"t1","current_agent_run_id":"r2"}`,
		"GET /api/v1/tasks/t1/runs":         `[{"id":"r2","status":"waiting_human"}]`,
		"GET /api/v1/tasks/t1/runs/r2/logs": `[{"timestamp":"2026-01-01T00:00:00Z","type":"text","content":"hello"}]`,
		"GET /api/v1/tasks/t1/diff":         `{"branch":"ate/t1","diff":"+added"}`,
		"GET /api/v1/tasks/t2/diff":         `{"branch":"","diff":""}`,
		"POST /api/v1/tasks":                `{"id":"t9","label":"not_ready"}`,
	})
	tools := Select(RemoteTools)
	for _, tc := range []struct{ tool, args, want string }{
		{"list_repos", `{}`, `"name":"acme"`},
		{"list_workflows", `{}`, `"labels":["work","review"]`},
		{"get_task", `{"task_id":"t1"}`, `"current_agent_run_id":"r2"`},
		{"list_runs", `{"task_id":"t1"}`, "waiting_human"},
		{"get_run_logs", `{"task_id":"t1"}`, "hello"},
		{"get_diff", `{"task_id":"t1"}`, "+added"},
		{"get_diff", `{"task_id":"t2"}`, "No changes yet"},
		{"create_task", `{"title":"x","repo_id":"r1","label":"not_ready"}`, "t9"},
	} {
		text, isErr := c.Call(tools, tc.tool, json.RawMessage(tc.args))
		if isErr || !strings.Contains(text, tc.want) {
			t.Errorf("%s: got %q (err=%v)", tc.tool, text, isErr)
		}
	}
	if last := (*calls)[len(*calls)-1]; last.body["label"] != "not_ready" || last.body["workflow_id"] != nil {
		t.Errorf("create_task body = %v", last.body)
	}
	for _, tool := range []string{"get_task", "list_runs", "get_run_logs", "get_diff", "reply_to_run", "approve_task", "move_task"} {
		if _, isErr := c.Call(tools, tool, nil); !isErr {
			t.Errorf("%s accepted missing task_id", tool)
		}
	}
}

func TestDefsAndTruncate(t *testing.T) {
	defs := Defs(Select(RemoteTools))
	if len(defs) != len(RemoteTools) {
		t.Fatalf("got %d defs", len(defs))
	}
	ann := defs[0]["annotations"].(map[string]any)
	if ann["readOnlyHint"] != true {
		t.Errorf("list_repos should be read-only: %v", ann)
	}
	long := strings.Repeat("x", maxResultBytes+10)
	if got := truncate(long); len(got) >= len(long)+50 || !strings.Contains(got, "truncated") {
		t.Errorf("truncate: len %d", len(got))
	}
}
