package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// GeminiRunner runs the Gemini CLI (`gemini`) in headless mode
// (-p + --output-format stream-json). Verified against `gemini` v0.49.0's
// --help output and a live (unauthenticated) invocation; see
// docs/providers/gemini_cli.md for the full trace of what was confirmed.
//
// Gemini's stream-json event envelope ({"type":"init"|"message"|"tool_use"|
// "tool_result"|"result", ...}) is NOT the same schema as the claude/qwen
// stream-json envelope ({"type":"assistant"|"tool_use"|"tool_result"|
// "result", "message":{...}}), so this runner uses its own classifyGeminiJSON
// parser rather than reusing classifyStreamJSON.
//
// MCP servers are configured via a settings.json file read from
// $GEMINI_CLI_HOME/.gemini/settings.json (confirmed by reading the installed
// CLI's bundle: GEMINI_CLI_HOME overrides the default `~/.gemini` base dir).
// Because that's a *directory* the CLI reads on startup rather than a
// per-invocation flag, this runner points GEMINI_CLI_HOME at a fresh per-run
// temp directory (so concurrent runs on the same host never share/clobber a
// config file) and writes a `{"mcpServers": {...}}` settings.json into it,
// reusing the same mcpServerEntry shape MCPManager.Prepare produces for
// claude/qwen's --mcp-config.
type GeminiRunner struct {
	// Path to the gemini binary. Defaults to "gemini" (resolved via PATH).
	BinaryPath string
	MCP        *MCPManager
	// UploadDir is the server-side directory where task attachments are stored.
	// Reserved for future use if the Gemini CLI gains an --image flag.
	UploadDir string
	// BackendURL / APIToken let the create_subtask MCP tool post children live to
	// the backend REST API (same container). Set from server config.
	BackendURL string
	APIToken   string
}

func (r *GeminiRunner) binary() string {
	if r.BinaryPath != "" {
		return r.BinaryPath
	}
	return "gemini"
}

// geminiRunConfig carries the per-run GEMINI_CLI_HOME directory prepared for
// MCP server wiring, so Run can clean it up after the process exits.
type geminiRunConfig struct {
	HomeDir string
}

// prepareGeminiHome creates a per-run GEMINI_CLI_HOME directory containing
// .gemini/settings.json with the given MCP server entries, so the gemini CLI
// picks them up without touching any shared/global config on the host.
// Returns nil if there are no servers to configure (mcpCfg is nil).
func prepareGeminiHome(runID string, mcpEntry *mcpServerEntry) (*geminiRunConfig, error) {
	if mcpEntry == nil {
		return nil, nil
	}
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("ate-gemini-home-%s", runID))
	geminiDir := filepath.Join(dir, ".gemini")
	if err := os.MkdirAll(geminiDir, 0700); err != nil {
		return nil, fmt.Errorf("mkdir gemini home: %w", err)
	}
	settings := mcpConfig{MCPServers: map[string]mcpServerEntry{"task-editor": *mcpEntry}}
	data, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini settings: %w", err)
	}
	if err := os.WriteFile(filepath.Join(geminiDir, "settings.json"), data, 0600); err != nil {
		return nil, fmt.Errorf("write gemini settings: %w", err)
	}
	return &geminiRunConfig{HomeDir: dir}, nil
}

func (c *geminiRunConfig) Cleanup() {
	if c == nil {
		return
	}
	_ = os.RemoveAll(c.HomeDir)
}

// buildGeminiArgs constructs the CLI argument list for the gemini binary
// given the run input and whether MCP is configured. Extracted as a
// standalone function so the arg-construction logic can be unit tested
// without spawning a subprocess — mirrors buildQwenArgs in qwen.go.
//
// Notes on flags (verified against `gemini --help`, v0.49.0):
//   - Non-interactive mode: -p/--prompt.
//   - --output-format stream-json emits NDJSON events (see classifyGeminiJSON).
//   - --yolo auto-approves every tool call, required for unattended runs.
//   - --skip-trust bypasses the interactive "trust this folder?" prompt that
//     otherwise blocks MCP servers from loading in an untrusted workspace —
//     required for the MCP sidecar to be reachable in headless mode.
//   - There is no confirmed --max-turns-equivalent flag for the Gemini CLI's
//     non-interactive mode, so no turn cap is passed (see docs/providers/gemini_cli.md).
//   - There is no confirmed command allowlist/denylist flag; CommandAllowlist
//     and CommandDenylist are NOT enforced for this provider (documented gap,
//     same treatment as qwen's denylist gap).
func buildGeminiArgs(input RunInput, mcpConfigured bool) []string {
	args := []string{
		"-p", buildPrompt(input) + "\n\n" + buildSystemPrompt(input),
		"--output-format", "stream-json",
		"--yolo",
	}
	if mcpConfigured {
		// --skip-trust is required in headless mode: without it, the CLI blocks
		// MCP servers ("this folder is untrusted") and the sidecar tools would
		// silently be unavailable.
		args = append(args, "--skip-trust")
	}
	if input.AgentConfig.Model != "" {
		args = append(args, "--model", input.AgentConfig.Model)
	}
	// gemini bug #14180: with --resume, stdin/positional args are ignored — only
	// the -p/--prompt flag delivers the message. We already pass -p above, so fine.
	if input.ResumeSessionID != "" {
		args = append(args, "--resume", input.ResumeSessionID)
	}
	return args
}

func (r *GeminiRunner) Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error) {
	// Set up MCP sidecar if manager is configured. Unlike claude/qwen (which
	// take a per-invocation --mcp-config JSON file), the gemini CLI only reads
	// MCP servers from a settings.json under its "home" directory, so a fresh
	// GEMINI_CLI_HOME is prepared per run instead of reusing MCPManager.Prepare
	// directly for the CLI flag — but the sidecar's result-file protocol
	// (RUN_ID/RESULT_FILE/etc. env vars) is identical, so MCPManager.Prepare is
	// still used to produce the sidecar's env vars and result file.
	var mcpCfg *MCPRunConfig
	var geminiHome *geminiRunConfig
	if r.MCP != nil && r.MCP.ServerBinary != "" {
		var err error
		mcpCfg, err = r.MCP.Prepare(input.RunID, input.Transitions, input.OpenReviewComments, nil, &SubtaskEnv{
			BackendURL:  r.BackendURL,
			APIToken:    r.APIToken,
			TaskID:      input.Task.ID,
			Enabled:     input.AgentConfig.SubtasksEnabled,
			MaxSubtasks: input.AgentConfig.MaxSubtasks,
		})
		if err != nil {
			return Result{Status: "failed"}, fmt.Errorf("prepare mcp: %w", err)
		}
		defer mcpCfg.Cleanup()

		entry := mcpServerEntry{
			Type:    "stdio",
			Command: r.MCP.ServerBinary,
			Args:    []string{},
			Env:     mcpSidecarEnv(input, r.BackendURL, r.APIToken),
		}
		geminiHome, err = prepareGeminiHome(input.RunID, &entry)
		if err != nil {
			return Result{Status: "failed"}, fmt.Errorf("prepare gemini home: %w", err)
		}
		defer geminiHome.Cleanup()
	}

	args := buildGeminiArgs(input, geminiHome != nil)

	timeoutSecs := input.AgentConfig.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 600
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.binary(), args...)
	cmd.Dir = input.RepoPath
	env := mergeEnv(os.Environ(), input.AgentConfig.Env)
	if geminiHome != nil {
		env = append(env, "GEMINI_CLI_HOME="+geminiHome.HomeDir)
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
		return Result{Status: "failed"}, fmt.Errorf("start gemini: %w", err)
	}

	logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("started gemini pid=%d", cmd.Process.Pid), At: time.Now()}

	var (
		wg          sync.WaitGroup
		outcome     string
		sessionID   string
		rateLimited bool
		transient   bool
		usage       *runUsage
		mu          sync.Mutex
	)
	wg.Add(2)

	// Stream stdout (stream-json lines) — Gemini's own event schema.
	go func() {
		defer wg.Done()
		rawDump := openRawDump(input.RunID) // dev-only; see rawDump in claude.go
		defer rawDump.Close()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			rawDump.WriteLine(line)
			entry, parsed, u, class, sid := classifyGeminiJSON(line)
			logCh <- entry
			if parsed != "" {
				mu.Lock()
				outcome = parsed
				mu.Unlock()
			}
			if sid != "" {
				mu.Lock()
				sessionID = sid
				mu.Unlock()
			}
			if class == ClassNone {
				class = ClassifyLine(line)
			}
			switch class {
			case ClassRateLimit:
				mu.Lock()
				rateLimited = true
				mu.Unlock()
			case ClassTransient:
				mu.Lock()
				transient = true
				mu.Unlock()
			}
			if u != nil {
				mu.Lock()
				usage = u
				mu.Unlock()
			}
		}
	}()

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
			} else if isTransientLine(line) {
				mu.Lock()
				transient = true
				mu.Unlock()
			}
		}
	}()

	wg.Wait()
	err = cmd.Wait()

	if err != nil && runCtx.Err() == context.DeadlineExceeded {
		logCh <- LogEntry{Type: LogSystem, Content: "agent timed out", At: time.Now()}
		return Result{Status: "failed"}, &ErrTransient{Cause: fmt.Errorf("gemini run timed out")}
	}
	if err != nil {
		logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("gemini exited: %v", err), At: time.Now()}
		mu.Lock()
		rl := rateLimited
		tr := transient
		mu.Unlock()
		if rl {
			return Result{Status: "failed"}, &ErrRateLimit{Message: "gemini CLI 429: Request rejected by API rate limit"}
		}
		if tr {
			return Result{Status: "failed"}, &ErrTransient{Cause: fmt.Errorf("gemini CLI exited with transient infra error: %w", err)}
		}
	}

	mu.Lock()
	finalUsage := usage
	finalSession := sessionID
	mu.Unlock()

	// MCP result takes priority; fall back to OUTCOME text parsing if the
	// agent completed without calling signal_complete.
	if mcpCfg != nil {
		res := mcpCfg.ReadResult()
		if res.Outcome == "" && outcome != "" {
			res.Outcome = outcome
		}
		applyUsage(&res, finalUsage)
		res.SessionID = finalSession
		if err != nil && res.Outcome == "" {
			failed := Result{Status: "failed", SessionID: finalSession}
			applyUsage(&failed, finalUsage)
			return failed, nil
		}
		return res, nil
	}

	if err != nil && outcome == "" {
		failed := Result{Status: "failed", SessionID: finalSession}
		applyUsage(&failed, finalUsage)
		return failed, nil
	}
	res := Result{Status: "completed", Outcome: outcome, SessionID: finalSession}
	applyUsage(&res, finalUsage)
	return res, nil
}

// mcpSidecarEnv builds the env vars the mcp-server sidecar binary expects,
// identical to what MCPManager.Prepare configures for the claude/qwen
// --mcp-config entry — factored out here since GeminiRunner needs to embed
// them directly into a settings.json mcpServers entry rather than letting
// MCPManager write them into its own JSON file.
func mcpSidecarEnv(input RunInput, backendURL, apiToken string) map[string]string {
	transitionsJSON, _ := json.Marshal(input.Transitions)
	reviewCommentsJSON, _ := json.Marshal(input.OpenReviewComments)
	env := map[string]string{
		"RUN_ID":          input.RunID,
		"RESULT_FILE":     filepath.Join(os.TempDir(), fmt.Sprintf("ate-result-%s.json", input.RunID)),
		"TRANSITIONS":     string(transitionsJSON),
		"REVIEW_COMMENTS": string(reviewCommentsJSON),
	}
	if input.AgentConfig.SubtasksEnabled {
		env["SUBTASKS_ENABLED"] = "1"
		env["BACKEND_URL"] = backendURL
		env["TASK_ID"] = input.Task.ID
		env["API_TOKEN"] = apiToken
		env["MAX_SUBTASKS"] = fmt.Sprintf("%d", input.AgentConfig.MaxSubtasks)
	}
	return env
}

// classifyGeminiJSON parses one NDJSON line from `gemini --output-format
// stream-json`. Gemini's event schema (confirmed by reading the installed
// CLI's bundled source, v0.49.0) is:
//
//	{"type":"init","session_id":...,"model":...}
//	{"type":"message","role":"user"|"assistant","content":...,"delta":true}
//	{"type":"tool_use","tool_name":...,"tool_id":...,"parameters":...}
//	{"type":"tool_result","tool_id":...,"status":"success"|"error","output":...,"error":{...}}
//	{"type":"result","status":"success"|"error","stats":{...},"error":{...}}
//
// This is intentionally NOT the same envelope as claude/qwen's stream-json
// (different type names: "message" vs "assistant", "result.status" vs
// "result.subtype"/"is_error"), so it is parsed independently rather than
// reusing classifyStreamJSON.
//
// Returns the log entry, an optional outcome ("success"/"failure") parsed
// from an OUTCOME marker in the final assistant text (Gemini's JSON output
// has no separate free-text summary field to scan, unlike claude/qwen's
// "result" message text), token usage/cost (non-nil only for the terminal
// "result" event, when stats are present), a failure Classification derived
// from the typed terminal "result"/"error" event, and the session_id carried
// on the "init" event (Gemini does not repeat session_id on every event the
// way claude/qwen do).
func classifyGeminiJSON(line string) (LogEntry, string, *runUsage, Classification, string) {
	var envelope struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		Role      string `json:"role"`
		Content   string `json:"content"`
		Status    string `json:"status"`
		Error     *struct {
			Message string `json:"message"`
		} `json:"error"`
		Stats *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return LogEntry{Type: LogStdout, Content: line, At: time.Now()}, "", nil, ClassNone, ""
	}

	switch envelope.Type {
	case "init":
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, "", nil, ClassNone, envelope.SessionID
	case "message":
		if envelope.Role != "assistant" {
			return LogEntry{Type: LogStdout, Content: line, At: time.Now()}, "", nil, ClassNone, ""
		}
		return LogEntry{Type: LogStdout, Content: envelope.Content, At: time.Now()}, extractOutcome(envelope.Content), nil, ClassNone, ""
	case "tool_use":
		return LogEntry{Type: LogToolCall, Content: line, At: time.Now()}, "", nil, ClassNone, ""
	case "tool_result":
		class := ClassNone
		if envelope.Status == "error" && envelope.Error != nil {
			class = ClassifyLine(envelope.Error.Message)
		}
		return LogEntry{Type: LogToolResult, Content: line, At: time.Now()}, "", nil, class, ""
	case "error":
		// Non-fatal stream-level error (e.g. a mid-run reconnect notice); still
		// worth classifying since some of these carry the real 429/auth signal.
		msg := ""
		if envelope.Error != nil {
			msg = envelope.Error.Message
		}
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, "", nil, ClassifyLine(msg), ""
	case "result":
		var usage *runUsage
		if envelope.Stats != nil {
			usage = &runUsage{InputTokens: envelope.Stats.InputTokens, OutputTokens: envelope.Stats.OutputTokens}
			// Gemini's stream-json "result" event does not report a cost figure
			// (only token counts) — CostUSD is left at zero here rather than
			// estimated. See docs/providers/gemini_cli.md.
		}
		if envelope.Status != "error" {
			return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, "success", usage, ClassNone, ""
		}
		class := ClassGenuine
		if envelope.Error != nil {
			if c := ClassifyLine(envelope.Error.Message); c != ClassNone {
				class = c
			}
		}
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, "failure", usage, class, ""
	default:
		return LogEntry{Type: LogStdout, Content: line, At: time.Now()}, "", nil, ClassNone, ""
	}
}
