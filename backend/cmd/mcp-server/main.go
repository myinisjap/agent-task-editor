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
	"time"
)

// result mirrors agent.Result for JSON serialisation.
type result struct {
	Status           string            `json:"Status"`
	Outcome          string            `json:"Outcome,omitempty"`
	Message          *string           `json:"Message,omitempty"`
	Notes            *string           `json:"Notes,omitempty"`
	StoredInfo       *string           `json:"StoredInfo,omitempty"`
	ResolvedComments []resolvedComment `json:"ResolvedComments,omitempty"`
}

// resolvedComment mirrors agent.ResolvedComment.
type resolvedComment struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}

type transitionHint struct {
	ToLabel string `json:"to_label"`
	Path    string `json:"path"`
}

// reviewComment carries just enough of agent.ReviewComment for the sidecar to
// validate resolve_comment calls against the set of open comments.
type reviewComment struct {
	ID string `json:"id"`
}

// toolState accumulates per-run state across tool calls: task notes, stored
// info, resolved review comments, and the terminal result (once
// signal_complete/request_human has fired) so later resolve_comment calls can
// re-persist it with updated resolutions.
type toolState struct {
	notes      string
	storedInfo string
	resolved   []resolvedComment
	terminal   *result
	commentIDs map[string]bool
}

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

func main() {
	// Configure log level from LOG_LEVEL env var (default: INFO), consistent with cmd/server.
	logLevel := slog.LevelInfo
	if l := os.Getenv("LOG_LEVEL"); l != "" {
		_ = logLevel.UnmarshalText([]byte(l))
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	runID := os.Getenv("RUN_ID")
	resultFile := os.Getenv("RESULT_FILE")
	if runID == "" || resultFile == "" {
		slog.Error("RUN_ID and RESULT_FILE env vars required")
		os.Exit(1)
	}
	log := slog.With("run_id", runID)

	// Parse available transitions from env (set by MCPManager.Prepare).
	var transitions []transitionHint
	if raw := os.Getenv("TRANSITIONS"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &transitions)
	}

	// Parse open review comments from env (set by MCPManager.Prepare) so
	// resolve_comment can validate IDs against the set of open comments.
	st := &toolState{commentIDs: map[string]bool{}}
	if raw := os.Getenv("REVIEW_COMMENTS"); raw != "" {
		var comments []reviewComment
		_ = json.Unmarshal([]byte(raw), &comments)
		for _, c := range comments {
			if c.ID != "" {
				st.commentIDs[c.ID] = true
			}
		}
	}

	// create_subtask (Mechanism 2) is exposed only when the run's agent config
	// opted in. Unlike the deferred result-file tools, it writes live through the
	// backend REST API so children appear on the board mid-run.
	subtasks := subtaskConfig{
		enabled:     os.Getenv("SUBTASKS_ENABLED") == "1",
		backendURL:  os.Getenv("BACKEND_URL"),
		taskID:      os.Getenv("TASK_ID"),
		apiToken:    os.Getenv("API_TOKEN"),
		maxSubtasks: os.Getenv("MAX_SUBTASKS"),
	}

	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
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

		// Notifications have no id — no response required
		if req.ID == nil {
			continue
		}

		respond := func(res any) {
			if err := enc.Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: res}); err != nil {
				log.Error("mcp encode response", "err", err)
			}
		}
		respondErr := func(code int, msg string) {
			if err := enc.Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: msg}}); err != nil {
				log.Error("mcp encode error response", "err", err)
			}
		}

		switch req.Method {
		case "initialize":
			respond(map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "task-editor", "version": "1.0.0"},
			})

		case "tools/list":
			tools := []map[string]any{
				{
					"name":        "get_task_transitions",
					"description": "Returns the available workflow transitions from the task's current label. Call this first to know which outcome values are valid for signal_complete.",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{},
					},
				},
				{
					"name":        "signal_complete",
					"description": "Call when your work is done. Pass outcome='success' if the work succeeded or outcome='failure' if it did not. The system resolves the correct next workflow label automatically.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"outcome": map[string]any{"type": "string", "enum": []string{"success", "failure"}, "description": "Whether the work succeeded or failed"},
							"summary": map[string]any{"type": "string", "description": "Brief summary of what was done"},
						},
						"required": []string{"outcome", "summary"},
					},
				},
				{
					"name":        "request_human",
					"description": "Pause and request human input before continuing.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"message": map[string]any{"type": "string", "description": "Question or context for the human reviewer"},
						},
						"required": []string{"message"},
					},
				},
				{
					"name":        "update_task_notes",
					"description": "Write structured notes to the task for subsequent agents to read. Use this to record plans, analysis, review findings, or any context that the next agent in the workflow should have.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"notes":  map[string]any{"type": "string", "description": "The notes content (supports markdown)"},
							"append": map[string]any{"type": "boolean", "description": "If true, append to existing notes instead of replacing"},
						},
						"required": []string{"notes"},
					},
				},
				{
					"name":        "store_info",
					"description": "Store structured information about this run that will be visible in the task view after completion.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"info": map[string]any{"type": "string", "description": "Information to store (markdown or plain text)"},
						},
						"required": []string{"info"},
					},
				},
				{
					"name":        "resolve_comment",
					"description": "Mark an inline diff review comment (from the OPEN REVIEW COMMENTS section of your prompt) as addressed. Call once per comment after you have made the fix.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"comment_id": map[string]any{"type": "string", "description": "The comment_id from the OPEN REVIEW COMMENTS section"},
							"note":       map[string]any{"type": "string", "description": "One-line description of how the comment was addressed"},
						},
						"required": []string{"comment_id", "note"},
					},
				},
			}
			if subtasks.enabled {
				tools = append(tools, map[string]any{
					"name":        "create_subtask",
					"description": "Split this task into a smaller child task that a later agent will execute. Children run on their own branch (cut from this task's branch) and merge back automatically. Use during planning to decompose large work. Children land on a human-review label; a human releases them. Capped at " + subtasks.maxSubtasks + " per task.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title":       map[string]any{"type": "string", "description": "Short title of the subtask"},
							"description": map[string]any{"type": "string", "description": "What the subtask should accomplish"},
							"type":        map[string]any{"type": "string", "enum": []string{"feature", "bug", "chore", "spike"}, "description": "Task type (default feature)"},
						},
						"required": []string{"title", "description"},
					},
				})
			}
			respond(map[string]any{"tools": tools})

		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				respondErr(-32602, "invalid params")
				continue
			}

			// create_subtask writes live to the backend rather than the result file.
			if params.Name == "create_subtask" {
				text := createSubtask(subtasks, params.Arguments, log)
				respond(map[string]any{
					"content": []map[string]any{{"type": "text", "text": text}},
					"isError": false,
				})
				continue
			}

			text, r := dispatchTool(params.Name, params.Arguments, st, transitions)
			if r != nil {
				if data, err := json.Marshal(r); err == nil {
					if err := os.WriteFile(resultFile, data, 0600); err != nil {
						log.Error("write result file", "err", err)
					}
				}
			}
			respond(map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": false,
			})

		default:
			respondErr(-32601, "method not found")
		}
	}
}

// dispatchTool executes one tool call against the accumulated run state.
// The returned *result, when non-nil, must be persisted to RESULT_FILE by the
// caller — terminal tools (signal_complete/request_human) return the full
// result, and resolve_comment returns the current state so resolutions
// survive even if the agent never signals completion.
func dispatchTool(name string, args json.RawMessage, st *toolState, transitions []transitionHint) (string, *result) {
	switch name {
	case "get_task_transitions":
		if len(transitions) == 0 {
			return "No transitions configured for this label.", nil
		}
		data, _ := json.Marshal(transitions)
		return string(data), nil

	case "signal_complete":
		var a struct {
			Outcome string `json:"outcome"`
			Summary string `json:"summary"`
		}
		_ = json.Unmarshal(args, &a)
		msg := a.Summary
		r := &result{Status: "completed", Outcome: a.Outcome, Message: &msg}
		st.fill(r)
		st.terminal = r
		return "acknowledged", r

	case "request_human":
		var a struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(args, &a)
		msg := a.Message
		r := &result{Status: "waiting_human", Message: &msg}
		st.fill(r)
		st.terminal = r
		return "pausing for human input", r

	case "store_info":
		var a struct {
			Info string `json:"info"`
		}
		_ = json.Unmarshal(args, &a)
		st.storedInfo = a.Info
		return "stored", nil

	case "update_task_notes":
		var a struct {
			Notes  string `json:"notes"`
			Append bool   `json:"append"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Append && st.notes != "" {
			st.notes = st.notes + "\n\n" + a.Notes
		} else {
			st.notes = a.Notes
		}
		return "Task notes updated", nil

	case "resolve_comment":
		var a struct {
			CommentID string `json:"comment_id"`
			Note      string `json:"note"`
		}
		_ = json.Unmarshal(args, &a)
		if a.CommentID == "" {
			return "comment_id is required", nil
		}
		if len(st.commentIDs) > 0 && !st.commentIDs[a.CommentID] {
			return "unknown comment_id: " + a.CommentID + " (not in this task's open review comments)", nil
		}
		for _, rc := range st.resolved {
			if rc.ID == a.CommentID {
				return "comment already resolved", nil
			}
		}
		st.resolved = append(st.resolved, resolvedComment{ID: a.CommentID, Note: a.Note})
		// Persist immediately: if the run already signalled a terminal result,
		// re-write it with the updated resolutions; otherwise write a partial
		// result (no Status) so resolutions survive an agent that exits
		// without calling signal_complete.
		if st.terminal != nil {
			st.terminal.ResolvedComments = st.resolved
			return "comment resolved", st.terminal
		}
		return "comment resolved", &result{ResolvedComments: st.resolved}

	default:
		return "unknown tool: " + name, nil
	}
}

// subtaskConfig holds the backend coordinates for the create_subtask tool.
type subtaskConfig struct {
	enabled     bool
	backendURL  string
	taskID      string
	apiToken    string
	maxSubtasks string
}

// createSubtask posts a new child task to the backend REST API and returns a
// human-readable text result for the agent (the created id, or an error). It is
// live (synchronous) so the agent gets a real task id back mid-run.
func createSubtask(cfg subtaskConfig, args json.RawMessage, log *slog.Logger) string {
	if cfg.backendURL == "" || cfg.taskID == "" {
		return "create_subtask is not configured on this server"
	}
	var a struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Type        string `json:"type"`
	}
	_ = json.Unmarshal(args, &a)
	if a.Title == "" {
		return "title is required"
	}

	payload, _ := json.Marshal(map[string]string{
		"title":       a.Title,
		"description": a.Description,
		"type":        a.Type,
	})
	url := cfg.backendURL + "/api/v1/tasks/" + cfg.taskID + "/subtasks"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "failed to build request: " + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.apiToken)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Error("create_subtask request failed", "err", err)
		return "failed to reach backend: " + err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var created struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		}
		_ = json.Unmarshal(respBody, &created)
		return fmt.Sprintf("Created subtask %s on label %q.", created.ID, created.Label)
	}
	// Surface the backend's error message so the agent can adapt (e.g. cap hit).
	var errResp struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(respBody, &errResp)
	if errResp.Error != "" {
		return fmt.Sprintf("create_subtask failed (%d): %s", resp.StatusCode, errResp.Error)
	}
	return fmt.Sprintf("create_subtask failed (%d)", resp.StatusCode)
}

// fill copies the accumulated notes/stored-info/resolutions onto a terminal result.
func (st *toolState) fill(r *result) {
	if st.notes != "" {
		notes := st.notes
		r.Notes = &notes
	}
	if st.storedInfo != "" {
		info := st.storedInfo
		r.StoredInfo = &info
	}
	if len(st.resolved) > 0 {
		r.ResolvedComments = st.resolved
	}
}
