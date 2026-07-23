// Command mcp-board is a standing MCP (Model Context Protocol) server that lets
// a chat client (e.g. Claude Desktop) manage the Agent Task Editor board:
// discover repos/workflows and create tickets. It talks to the backend over its
// REST API, so it can run anywhere that can reach BACKEND_URL.
//
// It is deliberately separate from cmd/mcp-server (the per-run sidecar the
// in-flow kanban agents get). Those agents never see these tools — this binary
// is a long-lived process a human points their chat client at, whereas the
// sidecar is an ephemeral, task-scoped subprocess. That separation is the whole
// point: task creation lives here, not in the flow agents' toolset.
//
// Protocol: newline-delimited JSON-RPC 2.0 over stdio (initialize, tools/list,
// tools/call), mirroring cmd/mcp-server.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

type rpcRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// backend wraps the REST calls this server makes against the Task Editor API.
type backend struct {
	baseURL string
	token   string
	client  *http.Client
}

func main() {
	logLevel := slog.LevelInfo
	if l := os.Getenv("LOG_LEVEL"); l != "" {
		_ = logLevel.UnmarshalText([]byte(l))
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	baseURL := strings.TrimRight(os.Getenv("BACKEND_URL"), "/")
	if baseURL == "" {
		slog.Error("BACKEND_URL env var required (e.g. http://localhost:8080)")
		os.Exit(1)
	}
	be := &backend{
		baseURL: baseURL,
		token:   os.Getenv("API_TOKEN"),
		client:  &http.Client{Timeout: 30 * time.Second},
	}

	serve(os.Stdin, os.Stdout, be)
}

// serve runs the JSON-RPC loop. Split out from main so tests can drive it with
// in-memory pipes.
func serve(in io.Reader, out io.Writer, be *backend) {
	enc := json.NewEncoder(out)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		// Notifications (no id) require no response.
		if req.ID == nil {
			continue
		}

		respond := func(res any) {
			if err := enc.Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: res}); err != nil {
				slog.Error("encode response", "err", err)
			}
		}
		respondErr := func(code int, msg string) {
			if err := enc.Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: msg}}); err != nil {
				slog.Error("encode error response", "err", err)
			}
		}

		switch req.Method {
		case "initialize":
			respond(map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "task-editor-board", "version": "1.0.0"},
			})

		case "tools/list":
			respond(map[string]any{"tools": toolDefs()})

		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				respondErr(-32602, "invalid params")
				continue
			}
			text, isErr := be.callTool(params.Name, params.Arguments)
			respond(map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": isErr,
			})

		default:
			respondErr(-32601, "method not found")
		}
	}
}

// toolDefs is the fixed tool list this server advertises.
func toolDefs() []map[string]any {
	return []map[string]any{
		{
			"name":        "list_repos",
			"description": "List the repositories configured on the board. Use this to find the repo_id to pass to create_task.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name":        "list_workflows",
			"description": "List the workflows configured on the board, including each workflow's label (column) names. Use this to discover which labels a task can be created on (e.g. \"work\").",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name":        "create_task",
			"description": "Create a ticket on the board. By default the ticket lands on the \"work\" column so an agent starts on it immediately; pass a different label to stage it elsewhere (e.g. \"not_ready\"). If workflow_id is omitted, the board's default workflow is used (the one named \"Default\", else the alphabetically-first workflow).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       map[string]any{"type": "string", "description": "Short title of the ticket"},
					"description": map[string]any{"type": "string", "description": "What the ticket should accomplish (markdown supported)"},
					"type":        map[string]any{"type": "string", "enum": []string{"feature", "bug", "chore", "spike"}, "description": "Task type (default feature)"},
					"repo_id":     map[string]any{"type": "string", "description": "ID of the repo the ticket belongs to (from list_repos)"},
					"workflow_id": map[string]any{"type": "string", "description": "Workflow ID (from list_workflows); defaults to the board's default workflow when omitted"},
					"label":       map[string]any{"type": "string", "description": "Column the ticket starts on (default \"work\"). Must be a label in the workflow."},
				},
				"required": []string{"title", "repo_id"},
			},
		},
	}
}

// callTool dispatches one tool call and returns (text, isError).
func (be *backend) callTool(name string, args json.RawMessage) (string, bool) {
	switch name {
	case "list_repos":
		return be.listRepos()
	case "list_workflows":
		return be.listWorkflows()
	case "create_task":
		return be.createTask(args)
	default:
		return "unknown tool: " + name, true
	}
}

func (be *backend) listRepos() (string, bool) {
	var repos []struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		WorkflowID  *string `json:"workflow_id"`
		CloneStatus string  `json:"clone_status"`
	}
	if err := be.get("/api/v1/repos", &repos); err != nil {
		return "failed to list repos: " + err.Error(), true
	}
	out := make([]map[string]any, 0, len(repos))
	for _, r := range repos {
		wf := ""
		if r.WorkflowID != nil {
			wf = *r.WorkflowID
		}
		out = append(out, map[string]any{
			"id":           r.ID,
			"name":         r.Name,
			"workflow_id":  wf,
			"clone_status": r.CloneStatus,
		})
	}
	data, _ := json.Marshal(out)
	return string(data), false
}

func (be *backend) listWorkflows() (string, bool) {
	var wfs []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := be.get("/api/v1/workflows", &wfs); err != nil {
		return "failed to list workflows: " + err.Error(), true
	}
	out := make([]map[string]any, 0, len(wfs))
	for _, wf := range wfs {
		names := make([]string, 0, len(wf.Labels))
		for _, l := range wf.Labels {
			names = append(names, l.Name)
		}
		out = append(out, map[string]any{
			"id":     wf.ID,
			"name":   wf.Name,
			"labels": names,
		})
	}
	data, _ := json.Marshal(out)
	return string(data), false
}

func (be *backend) createTask(args json.RawMessage) (string, bool) {
	var a struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Type        string `json:"type"`
		RepoID      string `json:"repo_id"`
		WorkflowID  string `json:"workflow_id"`
		Label       string `json:"label"`
	}
	_ = json.Unmarshal(args, &a)
	if a.Title == "" {
		return "title is required", true
	}
	if a.RepoID == "" {
		return "repo_id is required (call list_repos to find it)", true
	}
	if a.Label == "" {
		a.Label = "work"
	}

	// workflow_id is optional: when omitted, the backend applies the board's
	// default workflow (the one named "Default", else the alphabetically-first
	// workflow).
	payload := map[string]any{
		"title":       a.Title,
		"description": a.Description,
		"type":        a.Type,
		"repo_id":     a.RepoID,
		"label":       a.Label,
	}
	if a.WorkflowID != "" {
		payload["workflow_id"] = a.WorkflowID
	}
	var created struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	if status, err := be.post("/api/v1/tasks", payload, &created); err != nil {
		return fmt.Sprintf("create_task failed (%d): %s", status, err.Error()), true
	}
	return fmt.Sprintf("Created task %s on label %q.", created.ID, created.Label), false
}

// --- REST helpers ---

func (be *backend) get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, be.baseURL+path, nil)
	if err != nil {
		return err
	}
	be.auth(req)
	resp, err := be.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s -> %d: %s", path, resp.StatusCode, apiError(body))
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// post sends a JSON body and returns the HTTP status alongside any error, so the
// caller can surface the backend's status code to the agent.
func (be *backend) post(path string, payload, out any) (int, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, be.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	be.auth(req)
	resp, err := be.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s", apiError(body))
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (be *backend) auth(req *http.Request) {
	if be.token != "" {
		req.Header.Set("Authorization", "Bearer "+be.token)
	}
}

// apiError pulls the {"error": "..."} message the backend returns, falling back
// to the raw body.
func apiError(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(body))
}
