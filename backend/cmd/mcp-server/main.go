package main

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
)

// result mirrors agent.Result for JSON serialisation.
type result struct {
	Status     string  `json:"Status"`
	Outcome    string  `json:"Outcome,omitempty"`
	Message    *string `json:"Message,omitempty"`
	Notes      *string `json:"Notes,omitempty"`
	StoredInfo *string `json:"StoredInfo,omitempty"`
}

type transitionHint struct {
	ToLabel string `json:"to_label"`
	Path    string `json:"path"`
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

	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	var currentNotes string
	var currentStoredInfo string

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
			respond(map[string]any{
				"tools": []map[string]any{
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
				},
			})

		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				respondErr(-32602, "invalid params")
				continue
			}

			text, r := dispatchTool(params.Name, params.Arguments, &currentNotes, &currentStoredInfo, transitions)
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

func dispatchTool(name string, args json.RawMessage, currentNotes, currentStoredInfo *string, transitions []transitionHint) (string, *result) {
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
		if *currentNotes != "" {
			r.Notes = currentNotes
		}
		if *currentStoredInfo != "" {
			r.StoredInfo = currentStoredInfo
		}
		return "acknowledged", r

	case "request_human":
		var a struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(args, &a)
		msg := a.Message
		r := &result{Status: "waiting_human", Message: &msg}
		if *currentNotes != "" {
			r.Notes = currentNotes
		}
		if *currentStoredInfo != "" {
			r.StoredInfo = currentStoredInfo
		}
		return "pausing for human input", r

	case "store_info":
		var a struct {
			Info string `json:"info"`
		}
		_ = json.Unmarshal(args, &a)
		*currentStoredInfo = a.Info
		return "stored", nil

	case "update_task_notes":
		var a struct {
			Notes  string `json:"notes"`
			Append bool   `json:"append"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Append && *currentNotes != "" {
			*currentNotes = *currentNotes + "\n\n" + a.Notes
		} else {
			*currentNotes = a.Notes
		}
		return "Task notes updated", nil

	default:
		return "unknown tool: " + name, nil
	}
}
