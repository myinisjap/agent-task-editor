package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newBackend points a backend at a test server.
func newBackend(url, token string) *backend {
	return &backend{baseURL: strings.TrimRight(url, "/"), token: token, client: &http.Client{Timeout: 5 * time.Second}}
}

// call drives one tool call through serve() and returns the decoded result map.
func call(t *testing.T, be *backend, name string, args map[string]any) map[string]any {
	t.Helper()
	argsJSON, _ := json.Marshal(args)
	reqLine, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": json.RawMessage(argsJSON)},
	})
	var out bytes.Buffer
	serve(bytes.NewReader(append(reqLine, '\n')), &out, be)

	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (raw %q)", err, out.String())
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("no content in response: %q", out.String())
	}
	return map[string]any{"text": resp.Result.Content[0].Text, "isError": resp.Result.IsError}
}

func TestToolsList(t *testing.T) {
	reqLine, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	var out bytes.Buffer
	serve(bytes.NewReader(append(reqLine, '\n')), &out, newBackend("http://unused", ""))

	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range resp.Result.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"list_repos", "list_workflows", "create_task"} {
		if !got[want] {
			t.Errorf("tools/list missing %q; got %v", want, got)
		}
	}
}

func TestCreateTask_DefaultsToWork_DerivesWorkflowFromRepo(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/repo-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "repo-1", "workflow_id": "wf-9"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "task-abc", "label": gotBody["label"]})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	res := call(t, newBackend(srv.URL, ""), "create_task", map[string]any{
		"title":   "Add rate limiting",
		"repo_id": "repo-1",
	})
	if res["isError"].(bool) {
		t.Fatalf("unexpected error: %v", res["text"])
	}
	if gotBody["label"] != "work" {
		t.Errorf("expected default label 'work', got %v", gotBody["label"])
	}
	if gotBody["workflow_id"] != "wf-9" {
		t.Errorf("expected workflow derived from repo (wf-9), got %v", gotBody["workflow_id"])
	}
	if !strings.Contains(res["text"].(string), "task-abc") {
		t.Errorf("expected created id in result, got %v", res["text"])
	}
}

func TestCreateTask_ExplicitLabelAndWorkflow(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/tasks" {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "task-xyz", "label": gotBody["label"]})
			return
		}
		t.Errorf("unexpected request %s %s (workflow_id was provided, no repo lookup expected)", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	res := call(t, newBackend(srv.URL, ""), "create_task", map[string]any{
		"title":       "Stage this",
		"repo_id":     "repo-1",
		"workflow_id": "wf-2",
		"label":       "not_ready",
	})
	if res["isError"].(bool) {
		t.Fatalf("unexpected error: %v", res["text"])
	}
	if gotBody["label"] != "not_ready" || gotBody["workflow_id"] != "wf-2" {
		t.Errorf("body mismatch: %v", gotBody)
	}
}

func TestCreateTask_MissingTitle(t *testing.T) {
	res := call(t, newBackend("http://unused", ""), "create_task", map[string]any{"repo_id": "repo-1"})
	if !res["isError"].(bool) {
		t.Fatalf("expected error for missing title")
	}
}

func TestCreateTask_BackendErrorSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/tasks" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "label \"nope\" is not defined in this workflow"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	res := call(t, newBackend(srv.URL, ""), "create_task", map[string]any{
		"title":       "x",
		"repo_id":     "repo-1",
		"workflow_id": "wf-2",
		"label":       "nope",
	})
	if !res["isError"].(bool) {
		t.Fatalf("expected error result")
	}
	if !strings.Contains(res["text"].(string), "not defined in this workflow") {
		t.Errorf("expected backend error surfaced, got %v", res["text"])
	}
}

func TestListRepos_SendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "repo-1", "name": "acme", "workflow_id": "wf-9", "clone_status": "ready"}})
	}))
	defer srv.Close()

	res := call(t, newBackend(srv.URL, "secret-token"), "list_repos", map[string]any{})
	if res["isError"].(bool) {
		t.Fatalf("unexpected error: %v", res["text"])
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("expected bearer token header, got %q", gotAuth)
	}
	if !strings.Contains(res["text"].(string), "acme") {
		t.Errorf("expected repo name in output, got %v", res["text"])
	}
}
