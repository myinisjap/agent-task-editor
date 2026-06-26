package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// JSONRPCRequest represents a standard JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents a standard JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any            `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError  `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// CallMCP sends a JSON-RPC request to an already running MCP server.
func CallMCP(ctx context.Context, stdin io.WriteCloser, stdout io.Reader, method string, params any) (string, error) {
	paramBytes, _ := json.Marshal(params)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  paramBytes,
	}
	reqBytes, _ := json.Marshal(req)

	_, err := stdin.Write(append(reqBytes, '\n'))
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(stdout)
	if scanner.Scan() {
		var resp JSONRPCResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			return "", err
		}
		if resp.Error != nil {
			return "", fmt.Errorf("rpc error: %s", resp.Error.Message)
		}
		var result map[string]any
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return "", err
		}
		return fmt.Sprintf("%v", result), nil
	}

	return "", fmt.Errorf("no response from MCP server")
}
