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

var anthropicHTTPClient = &http.Client{Timeout: 120 * time.Second}

const anthropicDefaultBase = "https://api.anthropic.com"
const anthropicVersion = "2023-06-01"

// AnthropicRunner calls the Anthropic Messages API directly.
// It requires no external binaries — only ANTHROPIC_API_KEY.
// signal_complete and request_human are handled as native tools.
type AnthropicRunner struct {
	// APIKey is the Anthropic API key (typically from ANTHROPIC_API_KEY env var).
	APIKey string
	// BaseURL defaults to https://api.anthropic.com. Override for proxies/testing.
	BaseURL string
}

func (r *AnthropicRunner) base() string {
	if r.BaseURL != "" {
		return r.BaseURL
	}
	return anthropicDefaultBase
}

// tool definitions for the Anthropic Messages API (input_schema, not parameters)
var anthropicTools = []map[string]any{
	{
		"name":        "read_file",
		"description": "Read a file from the repository.",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string", "description": "File path relative to repo root"}},
			"required":   []string{"path"},
		},
	},
	{
		"name":        "write_file",
		"description": "Write or overwrite a file in the repository.",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
	},
	{
		"name":        "run_bash",
		"description": "Run a shell command in the repository directory.",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"command": map[string]any{"type": "string"}},
			"required":   []string{"command"},
		},
	},
	{
		"name":        "list_files",
		"description": "List files in a directory relative to the repository root.",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string", "description": "Directory path relative to repo root (empty for root)"}},
			"required":   []string{},
		},
	},
	{
		"name":        "store_info",
		"description": "Store structured information about this run that will be visible in the task view after completion.",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"info": map[string]any{"type": "string", "description": "Information to store (markdown or plain text)"}},
			"required":   []string{"info"},
		},
	},
	{
		"name":        "update_task_notes",
		"description": "Write structured notes to the task for subsequent agents to read. Use this to record plans, analysis, review findings, or any context that the next agent in the workflow should have.",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{
				"notes":  map[string]any{"type": "string", "description": "The notes content (supports markdown)"},
				"append": map[string]any{"type": "boolean", "description": "If true, append to existing notes instead of replacing"},
			},
			"required": []string{"notes"},
		},
	},
	{
		"name":        "signal_complete",
		"description": "Call when your work is done. Advances the task to the next workflow stage.",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{
				"next_label": map[string]any{"type": "string", "description": "The workflow label to move the task to"},
				"summary":    map[string]any{"type": "string", "description": "Brief summary of what was done"},
			},
			"required": []string{"next_label", "summary"},
		},
	},
	{
		"name":        "request_human",
		"description": "Pause and request human input before continuing.",
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"message": map[string]any{"type": "string"}},
			"required":   []string{"message"},
		},
	},
}

// anthropicMessage mirrors the Anthropic Messages API request/response schema.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []anthropicContent
}

type anthropicContent struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	ID         string          `json:"id,omitempty"`          // tool_use
	Name       string          `json:"name,omitempty"`        // tool_use
	Input      json.RawMessage `json:"input,omitempty"`       // tool_use
	ToolUseID  string          `json:"tool_use_id,omitempty"` // tool_result
	Content    string          `json:"content,omitempty"`     // tool_result
}

type anthropicResponse struct {
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r *AnthropicRunner) Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error) {
	timeoutSecs := input.AgentConfig.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 600
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	maxTokens := int(input.AgentConfig.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 8192
	}

	messages := []anthropicMessage{
		{Role: "user", Content: buildPrompt(input)},
	}

	var acc runAccumulators
	maxTurns := int(input.AgentConfig.MaxTurns)
	if maxTurns <= 0 {
		maxTurns = 50
	}
	for turn := 0; turn < maxTurns; turn++ {
		resp, err := r.messagesComplete(runCtx, input.AgentConfig.Model, buildSystemPrompt(input), maxTokens, messages)
		if err != nil {
			var rl *ErrRateLimit
			if errors.As(err, &rl) {
				return Result{Status: "failed"}, rl
			}
			return Result{Status: "failed"}, fmt.Errorf("anthropic messages turn %d: %w", turn, err)
		}

		// Collect text output and tool use blocks
		var toolUses []anthropicContent
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				if block.Text != "" {
					logCh <- LogEntry{Type: LogStdout, Content: block.Text, At: time.Now()}
				}
			case "tool_use":
				logCh <- LogEntry{Type: LogToolCall, Content: fmt.Sprintf("%s(%s)", block.Name, block.Input), At: time.Now()}
				toolUses = append(toolUses, block)
			}
		}

		// No tool calls — model finished
		if len(toolUses) == 0 || resp.StopReason == "end_turn" {
			res := Result{Status: "completed"}
			acc.attach(&res)
			return res, nil
		}

		// Append assistant turn
		messages = append(messages, anthropicMessage{Role: "assistant", Content: resp.Content})

		// Execute tools and build tool_result blocks
		var resultBlocks []anthropicContent
		for _, tu := range toolUses {
			var args map[string]json.RawMessage
			_ = json.Unmarshal(tu.Input, &args)
			strArgs := make(map[string]string, len(args))
			for k, v := range args {
				var s string
				if err := json.Unmarshal(v, &s); err == nil {
					strArgs[k] = s
				}
			}

			output, handled := acc.applySpecialTool(tu.Name, strArgs, tu.Input)
			var signal *Result
			if !handled {
				output, signal = executeLLMTool(runCtx, input.RepoPath, tu.Name, strArgs)
			}
			logCh <- LogEntry{Type: LogToolResult, Content: output, At: time.Now()}

			resultBlocks = append(resultBlocks, anthropicContent{
				Type:      "tool_result",
				ToolUseID: tu.ID,
				Content:   output,
			})

			if signal != nil {
				acc.attach(signal)
				return *signal, nil
			}
		}

		messages = append(messages, anthropicMessage{Role: "user", Content: resultBlocks})
	}

	return Result{Status: "failed"}, fmt.Errorf("exceeded max turns (%d)", maxTurns)
}

// parseAnthropicRateLimitReset tries to read a reset time from Anthropic rate-limit headers.
// It checks anthropic-ratelimit-requests-reset, anthropic-ratelimit-tokens-reset, and retry-after.
// Returns zero time if none are usable.
func parseAnthropicRateLimitReset(h http.Header) time.Time {
	for _, key := range []string{
		"anthropic-ratelimit-requests-reset",
		"anthropic-ratelimit-tokens-reset",
	} {
		if v := h.Get(key); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				return t
			}
		}
	}
	if v := h.Get("retry-after"); v != "" {
		// Try seconds integer first
		if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return time.Now().Add(time.Duration(secs) * time.Second)
		}
		// Try HTTP date
		if t, err := http.ParseTime(v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (r *AnthropicRunner) messagesComplete(ctx context.Context, model, system string, maxTokens int, messages []anthropicMessage) (anthropicResponse, error) {
	if model == "" {
		model = "claude-sonnet-5"
	}

	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages":   messages,
		"tools":      anthropicTools,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base()+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return anthropicResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", r.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := anthropicHTTPClient.Do(req)
	if err != nil {
		return anthropicResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 429 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return anthropicResponse{}, &ErrRateLimit{
			ResetAt: parseAnthropicRateLimitReset(resp.Header),
			Message: strings.TrimSpace(fmt.Sprintf("http 429: %s", body)),
		}
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return anthropicResponse{}, fmt.Errorf("http %d: %s", resp.StatusCode, body)
	}

	var result anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return anthropicResponse{}, fmt.Errorf("decode response: %w", err)
	}
	if result.Error != nil {
		return anthropicResponse{}, fmt.Errorf("anthropic api error (%s): %s", result.Error.Type, result.Error.Message)
	}
	return result, nil
}
