package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var llmHTTPClient = &http.Client{Timeout: 120 * time.Second}

// LLMRunner implements Provider using a raw OpenAI-compatible HTTP API.
// It runs a tool-use loop until signal_complete or request_human is called.
type LLMRunner struct {
	// BaseURL for the API (e.g. "https://api.openai.com/v1" or Anthropic endpoint).
	BaseURL string
	// APIKey is sent as Bearer token.
	APIKey  string
}

// tool definitions sent to the LLM
var llmTools = []map[string]any{
	{
		"type": "function",
		"function": map[string]any{
			"name":        "read_file",
			"description": "Read a file from the repository.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string", "description": "File path relative to repo root"}},
				"required":   []string{"path"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "write_file",
			"description": "Write or overwrite a file in the repository.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
				},
				"required": []string{"path", "content"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "run_bash",
			"description": "Run a shell command in the repository directory.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"command": map[string]any{"type": "string"}},
				"required":   []string{"command"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "list_files",
			"description": "List files in a directory.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string", "description": "Directory path relative to repo root (empty for root)"}},
				"required":   []string{},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "store_info",
			"description": "Store structured information about this run that will be visible in the task view after completion.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"info": map[string]any{"type": "string", "description": "Information to store (markdown or plain text)"}},
				"required":   []string{"info"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "update_task_notes",
			"description": "Write structured notes to the task for subsequent agents to read. Use this to record plans, analysis, review findings, or any context that the next agent in the workflow should have.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{
					"notes":  map[string]any{"type": "string", "description": "The notes content (supports markdown)"},
					"append": map[string]any{"type": "boolean", "description": "If true, append to existing notes instead of replacing"},
				},
				"required": []string{"notes"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "signal_complete",
			"description": "Call when your work is done. Advances the task to the next workflow stage.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{
					"next_label": map[string]any{"type": "string", "description": "The workflow label to move the task to"},
					"summary":    map[string]any{"type": "string", "description": "Brief summary of what was done"},
				},
				"required": []string{"next_label", "summary"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "request_human",
			"description": "Pause and request human input before continuing.",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"message": map[string]any{"type": "string"}},
				"required":   []string{"message"},
			},
		},
	},
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (r *LLMRunner) Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error) {
	timeoutSecs := input.AgentConfig.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 600
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	messages := []chatMessage{
		{Role: "system", Content: buildSystemPrompt(input)},
		{Role: "user", Content: buildPrompt(input)},
	}

	var acc runAccumulators
	maxTurns := int(input.AgentConfig.MaxTurns)
	if maxTurns <= 0 {
		maxTurns = 50
	}
	for turn := 0; turn < maxTurns; turn++ {
		resp, err := r.chatComplete(runCtx, input.AgentConfig.Model, messages)
		if err != nil {
			var rl *ErrRateLimit
			if errors.As(err, &rl) {
				return Result{Status: "failed"}, rl
			}
			return Result{Status: "failed"}, fmt.Errorf("chat complete turn %d: %w", turn, err)
		}

		if len(resp.ToolCalls) == 0 {
			// No tool calls — treat as completion
			logCh <- LogEntry{Type: LogStdout, Content: fmt.Sprintf("%v", resp.Content), At: time.Now()}
			res := Result{Status: "completed"}
			acc.attach(&res)
			return res, nil
		}

		// Append assistant message with tool calls
		messages = append(messages, chatMessage{Role: "assistant", ToolCalls: resp.ToolCalls})

		// Execute each tool call
		for _, tc := range resp.ToolCalls {
			logCh <- LogEntry{Type: LogToolCall, Content: fmt.Sprintf("%s(%s)", tc.Function.Name, tc.Function.Arguments), At: time.Now()}

			var args map[string]string
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

			output, handled := acc.applySpecialTool(tc.Function.Name, args, []byte(tc.Function.Arguments))
			var signal *Result
			if !handled {
				output, signal = r.executeTool(runCtx, input.RepoPath, tc)
			}

			logCh <- LogEntry{Type: LogToolResult, Content: output, At: time.Now()}

			messages = append(messages, chatMessage{
				Role:       "tool",
				Content:    output,
				ToolCallID: tc.ID,
			})

			if signal != nil {
				acc.attach(signal)
				return *signal, nil
			}
		}
	}

	return Result{Status: "failed"}, fmt.Errorf("exceeded max turns (%d)", maxTurns)
}

func (r *LLMRunner) executeTool(ctx context.Context, repoPath string, tc toolCall) (string, *Result) {
	var args map[string]string
	_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
	return executeLLMTool(ctx, repoPath, tc.Function.Name, args)
}

type completionResponse struct {
	Content   string
	ToolCalls []toolCall
}

// parseLLMRateLimitReset reads rate-limit reset info from standard retry-after and
// x-ratelimit-reset-requests / x-ratelimit-reset-tokens headers (OpenAI convention).
func parseLLMRateLimitReset(h http.Header) time.Time {
	for _, key := range []string{
		"x-ratelimit-reset-requests",
		"x-ratelimit-reset-tokens",
	} {
		if v := h.Get(key); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				return t
			}
		}
	}
	if v := h.Get("retry-after"); v != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return time.Now().Add(time.Duration(secs) * time.Second)
		}
		if t, err := http.ParseTime(v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (r *LLMRunner) chatComplete(ctx context.Context, model string, messages []chatMessage) (completionResponse, error) {
	body, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": messages,
		"tools":    llmTools,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", r.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return completionResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.APIKey)

	resp, err := llmHTTPClient.Do(req)
	if err != nil {
		// Network-level failure (DNS, connection refused, TLS, transport
		// timeout) — treat as transient so the pool can auto-retry rather
		// than surfacing it as a genuine task failure.
		return completionResponse{}, &ErrTransient{Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 429 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return completionResponse{}, &ErrRateLimit{
			ResetAt: parseLLMRateLimitReset(resp.Header),
			Message: strings.TrimSpace(fmt.Sprintf("http 429: %s", body)),
		}
	}
	if resp.StatusCode >= 500 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return completionResponse{}, &ErrTransient{Cause: fmt.Errorf("http %d: %s", resp.StatusCode, body)}
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return completionResponse{}, fmt.Errorf("http %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []toolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return completionResponse{}, fmt.Errorf("decode response: %w", err)
	}
	if result.Error != nil {
		return completionResponse{}, fmt.Errorf("api error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return completionResponse{}, fmt.Errorf("no choices in response")
	}

	msg := result.Choices[0].Message
	return completionResponse{Content: msg.Content, ToolCalls: msg.ToolCalls}, nil
}
