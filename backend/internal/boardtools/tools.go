// Package boardtools implements the board-level MCP tools (list/create tasks,
// read runs, approve, reply, ...) as thin wrappers over the backend's REST API.
//
// It is shared by two MCP front ends:
//   - cmd/mcp-board, the stdio server a local chat client (or the in-app Chat
//     tab) launches, which reaches the API over real HTTP; and
//   - internal/remotemcp, the streamable-HTTP endpoint a remote client such as
//     a claude.ai custom connector talks to, which calls the API router
//     in-process.
//
// Each front end picks which tools it exposes (see StdioTools / RemoteTools).
// None of these tools is ever handed to the in-flow kanban agents; those only
// get the per-run sidecar (cmd/mcp-server).
package boardtools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// maxResultBytes caps a single tool result so a huge diff or log page can't
// flood the chat client's context.
const maxResultBytes = 100 << 10

// Doer sends one API request. *http.Client satisfies it; the remote MCP
// endpoint supplies an in-process implementation instead.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client calls the backend REST API on behalf of the tools.
type Client struct {
	// BaseURL is prefixed to every API path (e.g. "http://localhost:8080").
	// Empty is fine for an in-process Doer that ignores the host.
	BaseURL string
	// Token, when non-empty, is sent as "Authorization: Bearer <token>".
	Token string
	HTTP  Doer
}

// Tool is one MCP tool: its advertised definition plus its implementation.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	// ReadOnly is surfaced as the MCP readOnlyHint annotation so clients can
	// skip confirmation for tools that change nothing.
	ReadOnly bool
	call     func(c *Client, args json.RawMessage) (string, bool)
}

// Def returns the tool's tools/list entry.
func (t Tool) Def() map[string]any {
	return map[string]any{
		"name":        t.Name,
		"description": t.Description,
		"inputSchema": t.InputSchema,
		"annotations": map[string]any{
			"readOnlyHint":    t.ReadOnly,
			"destructiveHint": false,
			"openWorldHint":   false,
		},
	}
}

// StdioTools is the set cmd/mcp-board exposes: ticket creation only, as it
// always has.
var StdioTools = []string{"list_repos", "list_workflows", "create_task"}

// RemoteTools is the set the remote MCP endpoint exposes: ticket creation plus
// reading tasks/runs and unblocking agents that wait on a human.
var RemoteTools = []string{
	"list_repos", "list_workflows", "list_tasks", "get_task", "list_runs", "get_run_logs", "get_diff",
	"create_task", "reply_to_run", "approve_task", "reject_task", "move_task",
}

// Select returns the tools with the given names, in that order. Unknown names
// panic: the lists above are fixed at compile time.
func Select(names []string) []Tool {
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		t, ok := registry[n]
		if !ok {
			panic("boardtools: unknown tool " + n)
		}
		out = append(out, t)
	}
	return out
}

// Defs returns the tools/list entries for tools.
func Defs(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Def())
	}
	return out
}

// Call runs the named tool if it is in tools, returning (text, isError).
func (c *Client) Call(tools []Tool, name string, args json.RawMessage) (string, bool) {
	for _, t := range tools {
		if t.Name == name {
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			return t.call(c, args)
		}
	}
	return "unknown tool: " + name, true
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var registry = map[string]Tool{}

func register(t Tool) { registry[t.Name] = t }

func init() {
	register(Tool{
		Name:        "list_repos",
		Description: "List the repositories configured on the board. Use this to find the repo_id to pass to create_task.",
		InputSchema: obj(map[string]any{}),
		ReadOnly:    true,
		call:        func(c *Client, _ json.RawMessage) (string, bool) { return c.listRepos() },
	})
	register(Tool{
		Name:        "list_workflows",
		Description: "List the workflows configured on the board, including each workflow's label (column) names. Use this to discover which labels a task can be created on (e.g. \"work\").",
		InputSchema: obj(map[string]any{}),
		ReadOnly:    true,
		call:        func(c *Client, _ json.RawMessage) (string, bool) { return c.listWorkflows() },
	})
	register(Tool{
		Name:        "create_task",
		Description: "Create a ticket on the board. By default the ticket lands on the \"work\" column so an agent starts on it immediately; pass a different label to stage it elsewhere (e.g. \"not_ready\"). If workflow_id is omitted, the board's default workflow is used (the one named \"Default\", else the alphabetically-first workflow).",
		InputSchema: obj(map[string]any{
			"title":       str("Short title of the ticket"),
			"description": str("What the ticket should accomplish (markdown supported)"),
			"type":        map[string]any{"type": "string", "enum": []string{"feature", "bug", "chore", "spike"}, "description": "Task type (default feature)"},
			"repo_id":     str("ID of the repo the ticket belongs to (from list_repos)"),
			"workflow_id": str("Workflow ID (from list_workflows); defaults to the board's default workflow when omitted"),
			"label":       str("Column the ticket starts on (default \"work\"). Must be a label in the workflow."),
		}, "title", "repo_id"),
		call: (*Client).createTask,
	})
	register(Tool{
		Name:        "list_tasks",
		Description: "List tasks on the board, newest first. Filter by free-text query, label (column), repo, or type. Archived tasks are excluded. Each entry includes the task's label, active run, PR URL and, when it can't be dispatched, why.",
		InputSchema: obj(map[string]any{
			"q":       str("Case-insensitive substring match against title and description"),
			"label":   str("Only tasks on this label/column (e.g. \"review\")"),
			"repo_id": str("Only tasks in this repo (from list_repos)"),
			"type":    map[string]any{"type": "string", "enum": []string{"feature", "bug", "chore", "spike"}},
			"limit":   map[string]any{"type": "integer", "description": "Max tasks to return (default 50, max 200)"},
		}),
		ReadOnly: true,
		call:     (*Client).listTasks,
	})
	register(Tool{
		Name:        "get_task",
		Description: "Get one task in full: description, label, agent notes, branch, PR URL, cost, and its active/current run ids.",
		InputSchema: obj(map[string]any{"task_id": str("Task ID")}, "task_id"),
		ReadOnly:    true,
		call: func(c *Client, args json.RawMessage) (string, bool) {
			a, err := taskArg(args)
			if err != "" {
				return err, true
			}
			return c.getRaw("/api/v1/tasks/"+url.PathEscape(a), "get_task")
		},
	})
	register(Tool{
		Name:        "list_runs",
		Description: "List a task's agent runs (newest first) with status, feedback, notes and cost. A run with status \"waiting_human\" is blocked on you; answer it with reply_to_run.",
		InputSchema: obj(map[string]any{"task_id": str("Task ID")}, "task_id"),
		ReadOnly:    true,
		call: func(c *Client, args json.RawMessage) (string, bool) {
			a, err := taskArg(args)
			if err != "" {
				return err, true
			}
			return c.getRaw("/api/v1/tasks/"+url.PathEscape(a)+"/runs", "list_runs")
		},
	})
	register(Tool{
		Name:        "get_run_logs",
		Description: "Get the most recent log entries of an agent run, oldest first. Use it to see what an agent did or why it failed.",
		InputSchema: obj(map[string]any{
			"task_id": str("Task ID"),
			"run_id":  str("Run ID (from list_runs or get_task); defaults to the task's current run"),
			"limit":   map[string]any{"type": "integer", "description": "Max log entries (default 100, max 500)"},
		}, "task_id"),
		ReadOnly: true,
		call:     (*Client).getRunLogs,
	})
	register(Tool{
		Name:        "get_diff",
		Description: "Get the task branch's changes as a unified diff against the ref it forked from. Empty if the agent hasn't started work yet.",
		InputSchema: obj(map[string]any{"task_id": str("Task ID")}, "task_id"),
		ReadOnly:    true,
		call:        (*Client).getDiff,
	})
	register(Tool{
		Name:        "reply_to_run",
		Description: "Answer an agent that is waiting on a human (a run with status \"waiting_human\") and let it continue. The task stays on its current label.",
		InputSchema: obj(map[string]any{
			"task_id": str("Task ID"),
			"message": str("Your answer to the agent's question"),
			"run_id":  str("The waiting run's ID; defaults to the task's active run"),
		}, "task_id", "message"),
		call: (*Client).replyToRun,
	})
	register(Tool{
		Name:        "approve_task",
		Description: "Approve a task waiting for human review: moves it along the workflow's \"success\" human transition from its current label.",
		InputSchema: obj(map[string]any{
			"task_id": str("Task ID"),
			"note":    str("Optional note recorded with the transition"),
		}, "task_id"),
		call: func(c *Client, args json.RawMessage) (string, bool) {
			return c.transition(args, "approve", false)
		},
	})
	register(Tool{
		Name:        "reject_task",
		Description: "Reject a task waiting for human review: moves it along the workflow's \"failure\" human transition (or to to_label), with a note telling the agent what to fix.",
		InputSchema: obj(map[string]any{
			"task_id":  str("Task ID"),
			"note":     str("What's wrong / what the agent should change"),
			"to_label": str("Optional explicit destination label"),
		}, "task_id"),
		call: func(c *Client, args json.RawMessage) (string, bool) {
			return c.transition(args, "reject", true)
		},
	})
	register(Tool{
		Name:        "move_task",
		Description: "Move a task to another label/column. The move must be a transition the workflow allows. Moving onto an agent-triggerable label (e.g. \"work\") starts an agent run.",
		InputSchema: obj(map[string]any{
			"task_id":  str("Task ID"),
			"to_label": str("Destination label"),
			"note":     str("Optional note recorded with the transition"),
		}, "task_id", "to_label"),
		call: (*Client).moveTask,
	})
}

func taskArg(args json.RawMessage) (string, string) {
	var a struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(args, &a)
	if strings.TrimSpace(a.TaskID) == "" {
		return "", "task_id is required"
	}
	return a.TaskID, ""
}

func (c *Client) listRepos() (string, bool) {
	var repos []struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		WorkflowID  *string `json:"workflow_id"`
		CloneStatus string  `json:"clone_status"`
	}
	if _, err := c.do(http.MethodGet, "/api/v1/repos", nil, &repos); err != nil {
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
	return marshal(out)
}

func (c *Client) listWorkflows() (string, bool) {
	var wfs []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if _, err := c.do(http.MethodGet, "/api/v1/workflows", nil, &wfs); err != nil {
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
	return marshal(out)
}

func (c *Client) createTask(args json.RawMessage) (string, bool) {
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
	if status, err := c.do(http.MethodPost, "/api/v1/tasks", payload, &created); err != nil {
		return fmt.Sprintf("create_task failed (%d): %s", status, err.Error()), true
	}
	return fmt.Sprintf("Created task %s on label %q.", created.ID, created.Label), false
}

func (c *Client) listTasks(args json.RawMessage) (string, bool) {
	var a struct {
		Q      string `json:"q"`
		Label  string `json:"label"`
		RepoID string `json:"repo_id"`
		Type   string `json:"type"`
		Limit  int    `json:"limit"`
	}
	_ = json.Unmarshal(args, &a)
	if a.Limit <= 0 {
		a.Limit = 50
	}
	if a.Limit > 200 {
		a.Limit = 200
	}
	qs := url.Values{}
	for k, v := range map[string]string{"q": a.Q, "label": a.Label, "repo_id": a.RepoID, "type": a.Type} {
		if v != "" {
			qs.Set(k, v)
		}
	}
	qs.Set("limit", strconv.Itoa(a.Limit))

	var tasks []struct {
		ID               string          `json:"id"`
		Title            string          `json:"title"`
		Type             string          `json:"type"`
		Label            string          `json:"label"`
		RepoID           string          `json:"repo_id"`
		ActiveAgentRunID *string         `json:"active_agent_run_id"`
		Paused           bool            `json:"paused"`
		GitState         string          `json:"git_state"`
		PrURL            string          `json:"pr_url"`
		UpdatedAt        string          `json:"updated_at"`
		BlockReason      json.RawMessage `json:"block_reason,omitempty"`
	}
	if _, err := c.do(http.MethodGet, "/api/v1/tasks?"+qs.Encode(), nil, &tasks); err != nil {
		return "failed to list tasks: " + err.Error(), true
	}
	return marshal(tasks)
}

func (c *Client) getRunLogs(args json.RawMessage) (string, bool) {
	var a struct {
		TaskID string `json:"task_id"`
		RunID  string `json:"run_id"`
		Limit  int    `json:"limit"`
	}
	_ = json.Unmarshal(args, &a)
	if a.TaskID == "" {
		return "task_id is required", true
	}
	if a.RunID == "" {
		var task struct {
			CurrentAgentRunID *string `json:"current_agent_run_id"`
		}
		if _, err := c.do(http.MethodGet, "/api/v1/tasks/"+url.PathEscape(a.TaskID), nil, &task); err != nil {
			return "failed to load task: " + err.Error(), true
		}
		if task.CurrentAgentRunID == nil {
			return "task has no runs yet", true
		}
		a.RunID = *task.CurrentAgentRunID
	}
	if a.Limit <= 0 {
		a.Limit = 100
	}
	if a.Limit > 500 {
		a.Limit = 500
	}
	var logs []struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Content   string `json:"content"`
	}
	path := fmt.Sprintf("/api/v1/tasks/%s/runs/%s/logs?limit=%d", url.PathEscape(a.TaskID), url.PathEscape(a.RunID), a.Limit)
	if _, err := c.do(http.MethodGet, path, nil, &logs); err != nil {
		return "failed to get run logs: " + err.Error(), true
	}
	return marshal(map[string]any{"run_id": a.RunID, "logs": logs})
}

func (c *Client) getDiff(args json.RawMessage) (string, bool) {
	id, errMsg := taskArg(args)
	if errMsg != "" {
		return errMsg, true
	}
	var d struct {
		Branch string `json:"branch"`
		Diff   string `json:"diff"`
	}
	if _, err := c.do(http.MethodGet, "/api/v1/tasks/"+url.PathEscape(id)+"/diff", nil, &d); err != nil {
		return "failed to get diff: " + err.Error(), true
	}
	if d.Diff == "" {
		return fmt.Sprintf("No changes yet on branch %q.", d.Branch), false
	}
	return truncate(fmt.Sprintf("Branch %s:\n%s", d.Branch, d.Diff)), false
}

func (c *Client) replyToRun(args json.RawMessage) (string, bool) {
	var a struct {
		TaskID  string `json:"task_id"`
		RunID   string `json:"run_id"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(args, &a)
	if a.TaskID == "" || strings.TrimSpace(a.Message) == "" {
		return "task_id and message are required", true
	}
	if a.RunID == "" {
		var task struct {
			ActiveAgentRunID *string `json:"active_agent_run_id"`
		}
		if _, err := c.do(http.MethodGet, "/api/v1/tasks/"+url.PathEscape(a.TaskID), nil, &task); err != nil {
			return "failed to load task: " + err.Error(), true
		}
		if task.ActiveAgentRunID == nil {
			return "task has no active run waiting for a reply", true
		}
		a.RunID = *task.ActiveAgentRunID
	}
	var res struct {
		RunID string `json:"run_id"`
	}
	path := fmt.Sprintf("/api/v1/tasks/%s/runs/%s/reply", url.PathEscape(a.TaskID), url.PathEscape(a.RunID))
	if status, err := c.do(http.MethodPost, path, map[string]any{"message": a.Message}, &res); err != nil {
		return fmt.Sprintf("reply_to_run failed (%d): %s", status, err.Error()), true
	}
	return fmt.Sprintf("Reply sent; the agent continues in run %s.", res.RunID), false
}

// transition drives approve/reject, which share a body shape.
func (c *Client) transition(args json.RawMessage, action string, allowTarget bool) (string, bool) {
	var a struct {
		TaskID  string `json:"task_id"`
		Note    string `json:"note"`
		ToLabel string `json:"to_label"`
	}
	_ = json.Unmarshal(args, &a)
	if a.TaskID == "" {
		return "task_id is required", true
	}
	body := map[string]any{"note": a.Note}
	if allowTarget && a.ToLabel != "" {
		body["to_label"] = a.ToLabel
	}
	var t struct {
		Label string `json:"label"`
	}
	if status, err := c.do(http.MethodPost, "/api/v1/tasks/"+url.PathEscape(a.TaskID)+"/"+action, body, &t); err != nil {
		return fmt.Sprintf("%s_task failed (%d): %s", action, status, err.Error()), true
	}
	return fmt.Sprintf("Task %s is now on %q.", a.TaskID, t.Label), false
}

func (c *Client) moveTask(args json.RawMessage) (string, bool) {
	var a struct {
		TaskID  string `json:"task_id"`
		ToLabel string `json:"to_label"`
		Note    string `json:"note"`
	}
	_ = json.Unmarshal(args, &a)
	if a.TaskID == "" || a.ToLabel == "" {
		return "task_id and to_label are required", true
	}
	var t struct {
		Label string `json:"label"`
	}
	body := map[string]any{"to_label": a.ToLabel, "note": a.Note}
	if status, err := c.do(http.MethodPatch, "/api/v1/tasks/"+url.PathEscape(a.TaskID)+"/label", body, &t); err != nil {
		return fmt.Sprintf("move_task failed (%d): %s", status, err.Error()), true
	}
	return fmt.Sprintf("Task %s is now on %q.", a.TaskID, t.Label), false
}

// getRaw passes an API response body through as the tool result.
func (c *Client) getRaw(path, tool string) (string, bool) {
	var raw json.RawMessage
	if _, err := c.do(http.MethodGet, path, nil, &raw); err != nil {
		return tool + " failed: " + err.Error(), true
	}
	return truncate(string(raw)), false
}

// --- REST helpers ---

// do sends one request (JSON body when payload is non-nil) and decodes a 2xx
// JSON response into out. It returns the HTTP status alongside any error so
// callers can surface the backend's status code.
func (c *Client) do(method, path string, payload, out any) (int, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s", apiError(respBody))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
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

func marshal(v any) (string, bool) {
	data, err := json.Marshal(v)
	if err != nil {
		return "failed to encode result: " + err.Error(), true
	}
	return truncate(string(data)), false
}

func truncate(s string) string {
	if len(s) <= maxResultBytes {
		return s
	}
	return s[:maxResultBytes] + fmt.Sprintf("\n… [truncated, %d bytes total]", len(s))
}
