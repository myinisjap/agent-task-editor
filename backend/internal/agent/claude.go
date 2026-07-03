package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ClaudeRunner runs the claude CLI in headless mode (-p + stream-json).
type ClaudeRunner struct {
	// Path to the claude binary. Defaults to "claude" (resolved via PATH).
	BinaryPath string
	MCP        *MCPManager
	// UploadDir is the server-side directory where task attachments are stored.
	// Used to resolve absolute paths when passing --image flags to the claude CLI.
	UploadDir string
}

func (r *ClaudeRunner) binary() string {
	if r.BinaryPath != "" {
		return r.BinaryPath
	}
	return "claude"
}

// buildClaudeArgs constructs the CLI argument list for the claude binary
// given the run input, whether the task-editor MCP sidecar is enabled, and
// the (optional) prepared MCP config. Extracted as a standalone function so
// the arg-construction logic (in particular the --max-turns default/override
// behavior) can be unit tested without spawning a subprocess.
func buildClaudeArgs(input RunInput, sidecarEnabled bool, mcpCfg *MCPRunConfig) ([]string, error) {
	var extraServerNames []string
	for _, name := range input.AgentConfig.EnabledMCPServers {
		if name == "" || name == "task-editor" {
			continue
		}
		extraServerNames = append(extraServerNames, name)
	}

	allowedTools := "Edit,Write,Read,Bash,Glob,Grep"
	if sidecarEnabled {
		allowedTools += ",mcp__task-editor__get_task_transitions,mcp__task-editor__signal_complete,mcp__task-editor__request_human,mcp__task-editor__update_task_notes,mcp__task-editor__store_info"
	}
	// Allow tools from each selected MCP server. Claude Code supports
	// server-level wildcarding via the bare "mcp__<server>" entry; this has
	// not been independently verified against a live CLI run and should be
	// double-checked if MCP tool calls are unexpectedly blocked.
	for _, name := range extraServerNames {
		allowedTools += ",mcp__" + name
	}

	settingsJSON, err := buildClaudeSettingsJSON(input.AgentConfig.EnabledPlugins, input.AgentConfig.CommandAllowlist, input.AgentConfig.CommandDenylist)
	if err != nil {
		return nil, fmt.Errorf("build claude settings: %w", err)
	}

	maxTurns := input.AgentConfig.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 50
	}

	args := []string{
		"-p", buildPrompt(input),
		"--system-prompt", buildSystemPrompt(input),
		"--output-format", "stream-json",
		"--verbose",
		"--allowedTools", allowedTools,
		"--max-turns", strconv.FormatInt(maxTurns, 10),
		"--bare",
		"--settings", settingsJSON,
	}
	if input.AgentConfig.Model != "" {
		args = append(args, "--model", input.AgentConfig.Model)
	}
	if mcpCfg != nil {
		args = append(args, "--mcp-config", mcpCfg.ConfigFile)
	}
	// Pass attachment images as --image flags so Claude can see them visually.
	for _, absPath := range input.AttachmentAbsPaths {
		args = append(args, "--image", absPath)
	}
	return args, nil
}

func (r *ClaudeRunner) Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error) {
	// Set up MCP sidecar if manager is configured. Additionally, if the agent
	// config has user-selected Claude MCP servers (from ~/.claude.json), merge
	// their raw config entries into the same --mcp-config file — even if the
	// task-editor sidecar itself is disabled — so selections still take effect.
	var mcpCfg *MCPRunConfig
	var extraServerNames []string
	for _, name := range input.AgentConfig.EnabledMCPServers {
		if name == "" || name == "task-editor" {
			continue
		}
		extraServerNames = append(extraServerNames, name)
	}

	sidecarEnabled := r.MCP != nil && r.MCP.ServerBinary != ""
	if sidecarEnabled || len(extraServerNames) > 0 {
		home, _ := os.UserHomeDir()
		extraServers := rawMCPServerConfigsFrom(home, extraServerNames)

		mgr := r.MCP
		if mgr == nil {
			// No task-editor sidecar configured, but the user selected MCP
			// servers — still produce an --mcp-config file with just those.
			mgr = &MCPManager{}
		}
		var err error
		mcpCfg, err = mgr.Prepare(input.RunID, input.Transitions, extraServers)
		if err != nil {
			return Result{Status: "failed"}, fmt.Errorf("prepare mcp: %w", err)
		}
		defer mcpCfg.Cleanup()
	}

	args, err := buildClaudeArgs(input, sidecarEnabled, mcpCfg)
	if err != nil {
		return Result{Status: "failed"}, err
	}

	timeoutSecs := input.AgentConfig.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 600
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.binary(), args...)
	cmd.Dir = input.RepoPath
	env := mergeEnv(os.Environ(), input.AgentConfig.Env)
	if tok := claudeOAuthToken(); tok != "" {
		env = append(env, "ANTHROPIC_AUTH_TOKEN="+tok)
	}
	cmd.Env = env

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
		wg           sync.WaitGroup
		outcome      string
		rateLimited  bool
		mu           sync.Mutex
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
			// Also scan stdout JSON lines for 429 indicators
			if is429Line(line) {
				mu.Lock()
				rateLimited = true
				mu.Unlock()
			}
		}
	}()

	// Stream stderr
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			logCh <- LogEntry{Type: LogStderr, Content: line, At: time.Now()}
			if is429Line(line) {
				mu.Lock()
				rateLimited = true
				mu.Unlock()
			}
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
		mu.Lock()
		rl := rateLimited
		mu.Unlock()
		if rl {
			return Result{Status: "failed"}, &ErrRateLimit{Message: "claude CLI 429: Request rejected by API rate limit"}
		}
	}

	// MCP result takes priority; fall back to OUTCOME text parsing if the
	// agent completed without calling signal_complete.
	if mcpCfg != nil {
		r := mcpCfg.ReadResult()
		if r.Outcome == "" && outcome != "" {
			r.Outcome = outcome
		}
		// Any non-zero exit from the claude binary means something went wrong
		// (e.g. auth error, crash, bad config). Even if a signal_complete outcome
		// was recorded, a non-zero exit overrides it — the agent may have signalled
		// before crashing, or the exit code may reflect an internal SDK error.
		if err != nil {
			if r.Outcome != "" {
				logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("claude exited with error but had outcome %q — treating as failed", r.Outcome), At: time.Now()}
			}
			return Result{Status: "failed"}, nil
		}
		return r, nil
	}

	// Non-zero exit means the agent failed regardless of any parsed outcome.
	// For example, claude exits 1 on auth errors, crashes, or internal failures.
	if err != nil {
		if outcome != "" {
			logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("claude exited with error but had parsed outcome %q — treating as failed", outcome), At: time.Now()}
		}
		return Result{Status: "failed"}, nil
	}
	return Result{Status: "completed", Outcome: outcome}, nil
}

// is429Line returns true if the log line indicates an API rate-limit rejection.
func is429Line(line string) bool {
	return strings.Contains(line, "429") ||
		strings.Contains(line, "Request rejected") ||
		strings.Contains(line, "rate limit") ||
		strings.Contains(line, "rate_limit")
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
		// Parse OUTCOME: success|failure from the result text; fall back to subtype.
		var outcome string
		if resultText, ok := raw["result"]; ok {
			var text string
			if err := json.Unmarshal(resultText, &text); err == nil {
				outcome = extractOutcome(text)
			}
		}
		if outcome == "" {
			subtype := strings.Trim(string(raw["subtype"]), `"`)
			if subtype == "success" {
				outcome = "success"
			} else if subtype == "error_max_turns" || subtype == "error" {
				outcome = "failure"
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
	if len(input.Task.Attachments) > 0 {
		b.WriteString("\n\nATTACHED IMAGES (available in .task_attachments/ within the repo):\n")
		for _, rel := range input.Task.Attachments {
			b.WriteString(fmt.Sprintf("- .task_attachments/%s\n", filepath.Base(rel)))
		}
	}
	return b.String()
}

func buildSystemPrompt(input RunInput) string {
	base := input.AgentConfig.SystemPrompt
	if base == "" {
		base = "You are an expert software engineer. Complete the assigned task thoroughly and carefully."
	}
	// Dynamically inject the repo working directory so the agent always knows where to work.
	var dirLine string
	if input.RepoPath != "" {
		dirLine = fmt.Sprintf("\n\nThe repository you are working on is located at: %s\nAll file operations should be performed relative to this directory.", input.RepoPath)
	}
	suffix := "\n\nIf the prompt contains a \"NOTES FROM PRIOR AGENT\" section, read it carefully before starting — it contains context, plans, and decisions from previous agents in this workflow.\n\nBefore calling mcp__task-editor__signal_complete, call mcp__task-editor__update_task_notes with a concise summary of what you did, what decisions you made, and any context the next agent will need. If prior notes exist (\"NOTES FROM PRIOR AGENT\" was present), use append:true to preserve them. This is how agents hand off state to each other — always do it.\n\nWhen your work is complete, call the mcp__task-editor__signal_complete tool with outcome='success' if the work succeeded or outcome='failure' if it did not. If the MCP tool is unavailable, end your final response with exactly: OUTCOME: success  or  OUTCOME: failure"
	return base + dirLine + suffix
}

// dangerousEnvKeys blocks user-supplied agent env vars from hijacking process execution.
var dangerousEnvKeys = map[string]bool{
	"PATH": true, "LD_PRELOAD": true, "LD_LIBRARY_PATH": true,
	"HOME": true, "SHELL": true, "IFS": true,
	"DYLD_INSERT_LIBRARIES": true, "DYLD_LIBRARY_PATH": true,
}

// buildClaudeSettingsJSON builds the JSON payload passed via --settings,
// defaulting every plugin installed on this machine to disabled and then
// enabling only those explicitly selected in enabledPlugins. If plugin
// discovery fails or returns nothing, falls back to a minimal settings
// object that just enables the selected plugins, so explicit selections
// still take effect even without a full inventory.
//
// commandAllowlist/commandDenylist, if non-empty, are translated into
// Claude Code's native "permissions.allow"/"permissions.deny" settings keys
// using "Bash(pattern)" entries — the same syntax Claude Code's
// --allowedTools/--disallowedTools flags accept. This is best-effort
// defense-in-depth (matched by the Claude CLI itself, not by this codebase),
// not a full sandbox.
//
// IMPORTANT (verified against a live claude binary, v2.1.198): "permissions.deny"
// reliably blocks matching Bash commands — commandDenylist is fully enforced.
// However "permissions.allow" only *auto-approves* matching commands and does
// NOT act as an exclusive allowlist: because the bare "Bash" tool is already
// granted via --allowedTools (required for the agent to run any command at
// all), a command that matches no commandAllowlist pattern is still permitted
// to run under permission-mode "default"/bypassPermissions — it is simply not
// auto-approved. There is currently no known claude CLI mechanism to make Bash
// itself default-deny while allowing only specific patterns. commandAllowlist
// is therefore NOT an effective restriction for the claude provider today; only
// commandDenylist should be relied on. See docs/providers/claude.md for detail.
func buildClaudeSettingsJSON(enabledPlugins, commandAllowlist, commandDenylist []string) (string, error) {
	selected := make(map[string]bool, len(enabledPlugins))
	for _, id := range enabledPlugins {
		if id != "" {
			selected[id] = true
		}
	}

	enabled := map[string]bool{}
	installed, err := ListInstalledClaudePlugins()
	if err == nil && len(installed) > 0 {
		for _, p := range installed {
			enabled[p.ID] = selected[p.ID]
		}
		// In case a selected plugin isn't in the discovered inventory (e.g. installed
		// after last discovery, or discovery is stale), still explicitly enable it.
		for id, on := range selected {
			if on {
				enabled[id] = true
			}
		}
	} else {
		for id, on := range selected {
			if on {
				enabled[id] = true
			}
		}
	}

	payload := map[string]any{"enabledPlugins": enabled}

	if len(commandAllowlist) > 0 || len(commandDenylist) > 0 {
		permissions := map[string]any{}
		if len(commandAllowlist) > 0 {
			allow := make([]string, 0, len(commandAllowlist))
			for _, p := range commandAllowlist {
				if p == "" {
					continue
				}
				allow = append(allow, "Bash("+p+")")
			}
			if len(allow) > 0 {
				permissions["allow"] = allow
			}
		}
		if len(commandDenylist) > 0 {
			deny := make([]string, 0, len(commandDenylist))
			for _, p := range commandDenylist {
				if p == "" {
					continue
				}
				deny = append(deny, "Bash("+p+")")
			}
			if len(deny) > 0 {
				permissions["deny"] = deny
			}
		}
		if len(permissions) > 0 {
			payload["permissions"] = permissions
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// claudeOAuthToken reads the access token from ~/.claude/.credentials.json for --bare mode.
func claudeOAuthToken() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(home + "/.claude/.credentials.json")
	if err != nil {
		return ""
	}
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return ""
	}
	return creds.ClaudeAiOauth.AccessToken
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
