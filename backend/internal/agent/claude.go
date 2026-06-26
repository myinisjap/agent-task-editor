package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
		mcpCfg, err = r.MCP.Prepare(input.RunID, input.Transitions)
		if err != nil {
			return Result{Status: "failed"}, fmt.Errorf("prepare mcp: %w", err)
		}
		defer mcpCfg.Cleanup()
	}

	allowedTools := "Edit,Write,Read,Bash,Glob,Grep"
	if mcpCfg != nil {
		allowedTools += ",mcp__task-editor__get_task_transitions,mcp__task-editor__signal_complete,mcp__task-editor__request_human,mcp__task-editor__update_task_notes,mcp__task-editor__store_info"
	}

	args := []string{
		"-p", buildPrompt(input),
		"--system-prompt", buildSystemPrompt(input),
		"--output-format", "stream-json",
		"--verbose",
		"--allowedTools", allowedTools,
		"--max-turns", "50",
		"--bare",
		"--settings", `{"enabledPlugins":{"oh-my-claudecode@omc":false}}`,
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

	var (
		wg      sync.WaitGroup
		outcome string
		mu      sync.Mutex
	)
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
			entry, parsed := classifyStreamJSON(line)
			logCh <- entry
			if parsed != "" {
				mu.Lock()
				outcome = parsed
				mu.Unlock()
			}
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

	// MCP result takes priority; fall back to OUTCOME text parsing if the
	// agent completed without calling signal_complete.
	if mcpCfg != nil {
		r := mcpCfg.ReadResult()
		if r.Outcome == "" && outcome != "" {
			r.Outcome = outcome
		}
		return r, nil
	}

	// Non-zero exit with no parsed outcome means the agent failed (e.g. error_max_turns).
	if err != nil && outcome == "" {
		return Result{Status: "failed"}, nil
	}
	return Result{Status: "completed", Outcome: outcome}, nil
}

// classifyStreamJSON parses one NDJSON line from claude --output-format stream-json.
// Returns the log entry and an optional next label parsed from a NEXT_LABEL marker.
func classifyStreamJSON(line string) (LogEntry, string) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return LogEntry{Type: LogStdout, Content: line, At: time.Now()}, ""
	}

	msgType := strings.Trim(string(raw["type"]), `"`)
	switch msgType {
	case "assistant":
		return LogEntry{Type: LogStdout, Content: extractAssistantText(raw), At: time.Now()}, ""
	case "tool_use":
		return LogEntry{Type: LogToolCall, Content: line, At: time.Now()}, ""
	case "tool_result":
		return LogEntry{Type: LogToolResult, Content: line, At: time.Now()}, ""
	case "user":
		// Claude SDK wraps tool results in a user message: {"type":"user","message":{"role":"user","content":[{"type":"tool_result",...}]}}
		return LogEntry{Type: LogToolResult, Content: line, At: time.Now()}, ""
	case "result":
		// Parse OUTCOME: success|failure from the result text
		var outcome string
		if resultText, ok := raw["result"]; ok {
			var text string
			if err := json.Unmarshal(resultText, &text); err == nil {
				outcome = extractOutcome(text)
			}
		}
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, outcome
	default:
		return LogEntry{Type: LogStdout, Content: line, At: time.Now()}, ""
	}
}

// extractOutcome looks for "OUTCOME: success|failure" anywhere in the text.
func extractOutcome(text string) string {
	const marker = "OUTCOME:"
	idx := strings.Index(text, marker)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(text[idx+len(marker):])
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	v := strings.ToLower(fields[0])
	if v == "success" || v == "failure" {
		return v
	}
	return ""
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
		b.WriteString("NOTES FROM PRIOR AGENT:\n")
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
	suffix := "\n\nIf the prompt contains a \"NOTES FROM PRIOR AGENT\" section, read it carefully before starting — it contains context, plans, and decisions from previous agents in this workflow.\n\nBefore calling mcp__task-editor__signal_complete, call mcp__task-editor__update_task_notes with a concise summary of what you did, what decisions you made, and any context the next agent will need. If prior notes exist (\"NOTES FROM PRIOR AGENT\" was present), use append:true to preserve them. This is how agents hand off state to each other — always do it.\n\nWhen your work is complete, call the mcp__task-editor__signal_complete tool with outcome='success' if the work succeeded or outcome='failure' if it did not. If the MCP tool is unavailable, end your final response with exactly: OUTCOME: success  or  OUTCOME: failure"
	return base + suffix
}

// dangerousEnvKeys blocks user-supplied agent env vars from hijacking process execution.
var dangerousEnvKeys = map[string]bool{
	"PATH": true, "LD_PRELOAD": true, "LD_LIBRARY_PATH": true,
	"HOME": true, "SHELL": true, "IFS": true,
	"DYLD_INSERT_LIBRARIES": true, "DYLD_LIBRARY_PATH": true,
}

func mergeEnv(base []string, extra map[string]string) []string {
	out := make([]string, len(base))
	copy(out, base)
	for k, v := range extra {
		if dangerousEnvKeys[strings.ToUpper(k)] {
			slog.Warn("agent env: blocked dangerous key", "key", k)
			continue
		}
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}
