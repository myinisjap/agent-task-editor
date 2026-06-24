package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ClaudeRunner runs the claude CLI in headless mode (-p + stream-json).
type ClaudeRunner struct {
	// Path to the claude binary. Defaults to "claude" (resolved via PATH).
	BinaryPath string
	MCP        *MCPManager
}

func (r *ClaudeRunner) binary() string {
	if r.BinaryPath != "" {
		return r.BinaryPath
	}
	return "claude"
}

func (r *ClaudeRunner) Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error) {
	// Set up MCP sidecar if manager is configured
	var mcpCfg *MCPRunConfig
	if r.MCP != nil && r.MCP.ServerBinary != "" {
		var err error
		mcpCfg, err = r.MCP.Prepare(input.RunID)
		if err != nil {
			return Result{Status: "failed"}, fmt.Errorf("prepare mcp: %w", err)
		}
		defer mcpCfg.Cleanup()
	}

	allowedTools := "Edit,Write,Read,Bash,Glob,Grep"
	if mcpCfg != nil {
		allowedTools += ",task-editor__signal_complete,task-editor__request_human"
	}

	args := []string{
		"-p", buildPrompt(input),
		"--system", buildSystemPrompt(input),
		"--output-format", "stream-json",
		"--allowedTools", allowedTools,
		"--max-turns", "50",
	}
	if input.AgentConfig.MaxTokens > 0 {
		args = append(args, "--max-tokens", fmt.Sprintf("%d", input.AgentConfig.MaxTokens))
	}
	if mcpCfg != nil {
		args = append(args, "--mcp-config", mcpCfg.ConfigFile)
	}

	timeoutSecs := input.AgentConfig.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 600
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.binary(), args...)
	cmd.Dir = input.RepoPath
	cmd.Env = mergeEnv(os.Environ(), input.AgentConfig.Env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{Status: "failed"}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{Status: "failed"}, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return Result{Status: "failed"}, fmt.Errorf("start claude: %w", err)
	}

	logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("started claude pid=%d", cmd.Process.Pid), At: time.Now()}

	var wg sync.WaitGroup
	wg.Add(2)

	// Stream stdout (stream-json lines)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			entry := classifyStreamJSON(line)
			logCh <- entry
		}
	}()

	// Stream stderr
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			logCh <- LogEntry{Type: LogStderr, Content: scanner.Text(), At: time.Now()}
		}
	}()

	wg.Wait()
	err = cmd.Wait()

	if err != nil && runCtx.Err() == context.DeadlineExceeded {
		logCh <- LogEntry{Type: LogSystem, Content: "agent timed out", At: time.Now()}
		return Result{Status: "failed"}, nil
	}
	if err != nil {
		logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("claude exited: %v", err), At: time.Now()}
	}

	// Read result from MCP sidecar output file
	if mcpCfg != nil {
		return mcpCfg.ReadResult(), nil
	}
	return Result{Status: "completed"}, nil
}

// classifyStreamJSON parses one NDJSON line from claude --output-format stream-json.
func classifyStreamJSON(line string) LogEntry {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return LogEntry{Type: LogStdout, Content: line, At: time.Now()}
	}

	msgType := strings.Trim(string(raw["type"]), `"`)
	switch msgType {
	case "assistant":
		// Extract text content for display
		return LogEntry{Type: LogStdout, Content: extractAssistantText(raw), At: time.Now()}
	case "tool_use":
		return LogEntry{Type: LogToolCall, Content: line, At: time.Now()}
	case "tool_result":
		return LogEntry{Type: LogToolResult, Content: line, At: time.Now()}
	case "result":
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}
	default:
		return LogEntry{Type: LogStdout, Content: line, At: time.Now()}
	}
}

func extractAssistantText(raw map[string]json.RawMessage) string {
	var msg struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw["message"], &msg.Message); err != nil {
		return string(raw["message"])
	}
	var parts []string
	for _, c := range msg.Message.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, " ")
}

func buildPrompt(input RunInput) string {
	var b strings.Builder
	if input.Feedback != nil && *input.Feedback != "" {
		b.WriteString("FEEDBACK FROM PRIOR REVIEW:\n")
		b.WriteString(*input.Feedback)
		b.WriteString("\n\n---\n\n")
	}
	if input.PriorPlan != nil && *input.PriorPlan != "" {
		b.WriteString("IMPLEMENTATION PLAN:\n")
		b.WriteString(*input.PriorPlan)
		b.WriteString("\n\n---\n\n")
	}
	b.WriteString(fmt.Sprintf("Task: %s\n\n", input.Task.Title))
	if input.Task.Description != "" {
		b.WriteString(input.Task.Description)
	}
	return b.String()
}

func buildSystemPrompt(input RunInput) string {
	base := input.AgentConfig.SystemPrompt
	if base == "" {
		base = "You are an expert software engineer. Complete the assigned task thoroughly and carefully."
	}
	return base + "\n\nWhen your work is complete, call the signal_complete tool with the next workflow label and a summary. If you need human input before continuing, call request_human."
}

func mergeEnv(base []string, extra map[string]string) []string {
	out := make([]string, len(base))
	copy(out, base)
	for k, v := range extra {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}
