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
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/boardtools"
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

// backend holds where and how this server reaches the Task Editor API; the
// tools themselves live in internal/boardtools.
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

// stdioTools is the fixed tool list this server advertises.
var stdioTools = boardtools.Select(boardtools.StdioTools)

// toolDefs is the tools/list payload.
func toolDefs() []map[string]any {
	return boardtools.Defs(stdioTools)
}

// callTool dispatches one tool call and returns (text, isError).
func (be *backend) callTool(name string, args json.RawMessage) (string, bool) {
	c := &boardtools.Client{BaseURL: be.baseURL, Token: be.token, HTTP: be.client}
	return c.Call(stdioTools, name, args)
}
