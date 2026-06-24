package main

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
)

// result mirrors agent.Result for JSON serialisation.
// Field names must match agent.Result (no json tags there, so default capitalized).
type result struct {
	Status    string  `json:"Status"`
	NextLabel *string `json:"NextLabel,omitempty"`
	Message   *string `json:"Message,omitempty"`
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
	runID := os.Getenv("RUN_ID")
	resultFile := os.Getenv("RESULT_FILE")
	if runID == "" || resultFile == "" {
		slog.Error("RUN_ID and RESULT_FILE env vars required")
		os.Exit(1)
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
			enc.Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: res})
		}
		respondErr := func(code int, msg string) {
			enc.Encode(rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: msg}})
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
						"name":        "signal_complete",
						"description": "Call when your work is done. Advances the task to the next workflow stage.",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"next_label": map[string]any{"type": "string", "description": "Workflow label to move the task to"},
								"summary":    map[string]any{"type": "string", "description": "Brief summary of what was done"},
							},
							"required": []string{"next_label", "summary"},
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
			text, r := dispatchTool(params.Name, params.Arguments)
			if r != nil {
				if data, err := json.Marshal(r); err == nil {
					os.WriteFile(resultFile, data, 0600)
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

func dispatchTool(name string, args json.RawMessage) (string, *result) {
	switch name {
	case "signal_complete":
		var a struct {
			NextLabel string `json:"next_label"`
			Summary   string `json:"summary"`
		}
		json.Unmarshal(args, &a)
		msg := a.Summary
		return "acknowledged", &result{Status: "completed", NextLabel: &a.NextLabel, Message: &msg}

	case "request_human":
		var a struct {
			Message string `json:"message"`
		}
		json.Unmarshal(args, &a)
		msg := a.Message
		return "pausing for human input", &result{Status: "waiting_human", Message: &msg}

	default:
		return "unknown tool: " + name, nil
	}
}
