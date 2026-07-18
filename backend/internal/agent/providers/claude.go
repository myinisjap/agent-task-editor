package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// ClaudeRunner runs the claude CLI in headless mode (-p + stream-json).
type ClaudeRunner struct {
	// Path to the claude binary. Defaults to "claude" (resolved via PATH).
	BinaryPath string
	MCP        *MCPManager
	// UploadDir is the server-side directory where task attachments are stored.
	// Used to resolve absolute paths when passing --image flags to the claude CLI.
	UploadDir string
	// BackendURL / APIToken let the create_subtask MCP tool post children live to
	// the backend REST API (same container). Set from server config.
	BackendURL string
	APIToken   string
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
func buildClaudeArgs(input agent.RunInput, sidecarEnabled bool, mcpCfg *MCPRunConfig) ([]string, error) {
	var extraServerNames []string
	for _, name := range input.AgentConfig.EnabledMCPServers {
		if name == "" || name == "task-editor" {
			continue
		}
		extraServerNames = append(extraServerNames, name)
	}

	allowedTools := "Edit,Write,Read,Bash,Glob,Grep"
	if sidecarEnabled {
		allowedTools += ",mcp__task-editor__get_task_transitions,mcp__task-editor__signal_complete,mcp__task-editor__request_human,mcp__task-editor__update_task_notes,mcp__task-editor__store_info,mcp__task-editor__resolve_comment"
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

	// A resumed session already contains the task context (title, description,
	// notes) as prior conversation turns, so only the new information (human
	// reply, feedback, open review comments) is sent as the next message.
	prompt := buildPrompt(input)
	if input.ResumeSessionID != "" {
		prompt = buildResumePrompt(input)
	}

	args := []string{
		"-p", prompt,
		"--system-prompt", buildSystemPrompt(input),
		"--output-format", "stream-json",
		"--verbose",
		"--allowedTools", allowedTools,
		"--max-turns", strconv.FormatInt(maxTurns, 10),
		"--bare",
		"--settings", settingsJSON,
	}
	if input.ResumeSessionID != "" {
		args = append(args, "--resume", input.ResumeSessionID)
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

func (r *ClaudeRunner) Run(ctx context.Context, input agent.RunInput, logCh chan<- agent.LogEntry) (agent.Result, error) {
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
		mcpCfg, err = mgr.Prepare(input.RunID, input.Transitions, input.OpenReviewComments, extraServers, &SubtaskEnv{
			BackendURL:  r.BackendURL,
			APIToken:    r.APIToken,
			TaskID:      input.Task.ID,
			Enabled:     input.AgentConfig.SubtasksEnabled,
			MaxSubtasks: input.AgentConfig.MaxSubtasks,
		})
		if err != nil {
			return agent.Result{Status: "failed"}, fmt.Errorf("prepare mcp: %w", err)
		}
		defer mcpCfg.Cleanup()
	}

	res, info, err := r.runAttempt(ctx, input, sidecarEnabled, mcpCfg, logCh)
	if input.ResumeSessionID != "" && shouldFallBackToColdStart(info) {
		// The --resume target most likely no longer exists (session expired,
		// CLI updated, state moved). Non-fatal: retry once from a cold start
		// with the full prompt.
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("could not resume session %s — starting a fresh session", input.ResumeSessionID), At: time.Now()}
		input.ResumeSessionID = ""
		res, _, err = r.runAttempt(ctx, input, sidecarEnabled, mcpCfg, logCh)
	}
	return res, err
}

// attemptInfo carries the signals runAttempt observed that Run needs to decide
// whether a failed --resume attempt should be retried as a cold start.
type attemptInfo struct {
	// sawStream is true once at least one well-formed stream-json line arrived —
	// evidence the CLI actually started a conversation.
	sawStream bool
	// resumeError is true when the output carried an explicit
	// session-not-found signal for the --resume target.
	resumeError bool
	// exitedWithError is true when the subprocess exited non-zero (for any reason).
	exitedWithError bool
}

// shouldFallBackToColdStart reports whether a resumed attempt failed in a way
// that points at the resume itself: either the CLI said the session doesn't
// exist, or it exited with an error before producing any stream output.
func shouldFallBackToColdStart(info attemptInfo) bool {
	if info.resumeError {
		return true
	}
	return info.exitedWithError && !info.sawStream
}

func (r *ClaudeRunner) runAttempt(ctx context.Context, input agent.RunInput, sidecarEnabled bool, mcpCfg *MCPRunConfig, logCh chan<- agent.LogEntry) (agent.Result, attemptInfo, error) {
	var info attemptInfo

	args, err := buildClaudeArgs(input, sidecarEnabled, mcpCfg)
	if err != nil {
		return agent.Result{Status: "failed"}, info, err
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
	if tok := ClaudeOAuthAccessToken(); tok != "" {
		env = append(env, "ANTHROPIC_AUTH_TOKEN="+tok)
	}
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.Result{Status: "failed"}, info, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agent.Result{Status: "failed"}, info, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agent.Result{Status: "failed"}, info, fmt.Errorf("start claude: %w", err)
	}

	logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("started claude pid=%d", cmd.Process.Pid), At: time.Now()}

	var (
		wg           sync.WaitGroup
		outcome      string
		sessionID    string
		rateLimited  bool
		rateLimitMsg string
		transient    bool
		usage        *runUsage
		mu           sync.Mutex
	)
	wg.Add(2)

	// Stream stdout (stream-json lines)
	go func() {
		defer wg.Done()
		// ponytail: dev-only raw capture to review/improve parsing. Set
		// AGENT_RAW_LOG_DIR to dump every stdout line verbatim to
		// <dir>/<runID>.jsonl. Off by default; no DB, no product surface.
		rawDump := openRawDump(input.RunID)
		defer rawDump.Close()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			rawDump.WriteLine(line)
			ev := classifyStreamJSON(line)
			logCh <- ev.Entry
			mu.Lock()
			if ev.Outcome != "" {
				outcome = ev.Outcome
			}
			if ev.Usage != nil {
				usage = ev.Usage
			}
			if ev.SessionID != "" {
				sessionID = ev.SessionID
				info.sawStream = true
			}
			if isResumeErrorLine(line) {
				info.resumeError = true
			}
			mu.Unlock()
			// Prefer the structured classification from the typed "result"
			// event; fall back to sniffing the raw line for non-result / non-
			// JSON output. See errclass.go.
			class := ev.Class
			if class == agent.ClassNone {
				class = agent.ClassifyLine(line)
			}
			switch class {
			case agent.ClassRateLimit:
				mu.Lock()
				rateLimited = true
				if ev.ResultText != "" {
					rateLimitMsg = ev.ResultText
				}
				mu.Unlock()
			case agent.ClassTransient:
				mu.Lock()
				transient = true
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
			logCh <- agent.LogEntry{Type: agent.LogStderr, Content: line, At: time.Now()}
			if isResumeErrorLine(line) {
				mu.Lock()
				info.resumeError = true
				mu.Unlock()
			}
			switch agent.ClassifyLine(line) {
			case agent.ClassRateLimit:
				mu.Lock()
				rateLimited = true
				mu.Unlock()
			case agent.ClassTransient:
				mu.Lock()
				transient = true
				mu.Unlock()
			}
		}
	}()

	wg.Wait()
	err = cmd.Wait()
	info.exitedWithError = err != nil

	mu.Lock()
	finalUsage := usage
	finalSession := sessionID
	mu.Unlock()

	if err != nil && runCtx.Err() == context.DeadlineExceeded {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: "agent timed out", At: time.Now()}
		// A timeout is ambiguous — it could be a genuinely stuck/looping agent
		// or a transient hang (e.g. network stall). Treat it as transient so
		// it counts against the task's bounded retry budget instead of
		// retrying forever unconditionally, but don't require an infra
		// signal to have been seen in the logs.
		return agent.Result{Status: "failed", SessionID: finalSession}, info, &agent.ErrTransient{Cause: fmt.Errorf("claude run timed out")}
	}
	if err != nil {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("claude exited: %v", err), At: time.Now()}
		mu.Lock()
		rl := rateLimited
		rlMsg := rateLimitMsg
		tr := transient
		mu.Unlock()
		if rl {
			message := "claude CLI 429: Request rejected by API rate limit"
			if rlMsg != "" {
				message = rlMsg
			}
			resetAt := parseClaudeResetTime(rlMsg, time.Now())
			if !resetAt.IsZero() {
				logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("claude rate limit resets at %s (retrying then)", resetAt.Format(time.RFC3339)), At: time.Now()}
			}
			return agent.Result{Status: "failed", SessionID: finalSession}, info, &agent.ErrRateLimit{ResetAt: resetAt, Message: message}
		}
		if tr {
			return agent.Result{Status: "failed", SessionID: finalSession}, info, &agent.ErrTransient{Cause: fmt.Errorf("claude CLI exited with transient infra error: %w", err)}
		}
	}

	// MCP result takes priority; fall back to OUTCOME text parsing if the
	// agent completed without calling signal_complete.
	if mcpCfg != nil {
		r := mcpCfg.ReadResult()
		if r.Outcome == "" && outcome != "" {
			r.Outcome = outcome
		}
		// ReadResult() (the MCP sidecar result file) has no knowledge of
		// token usage/cost — that only comes from the CLI's stream-json
		// "result" message — so merge it in here.
		applyUsage(&r, finalUsage)
		r.SessionID = finalSession
		// Any non-zero exit from the claude binary means something went wrong
		// (e.g. auth error, crash, bad config). Even if a signal_complete outcome
		// was recorded, a non-zero exit overrides it — the agent may have signalled
		// before crashing, or the exit code may reflect an internal SDK error.
		if err != nil {
			if r.Outcome != "" {
				logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("claude exited with error but had outcome %q — treating as failed", r.Outcome), At: time.Now()}
			}
			failed := agent.Result{Status: "failed", SessionID: finalSession}
			applyUsage(&failed, finalUsage)
			return failed, info, nil
		}
		return r, info, nil
	}

	// Non-zero exit means the agent failed regardless of any parsed outcome.
	// For example, claude exits 1 on auth errors, crashes, or internal failures.
	if err != nil {
		if outcome != "" {
			logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("claude exited with error but had parsed outcome %q — treating as failed", outcome), At: time.Now()}
		}
		failed := agent.Result{Status: "failed", SessionID: finalSession}
		applyUsage(&failed, finalUsage)
		return failed, info, nil
	}
	res := agent.Result{Status: "completed", Outcome: outcome, SessionID: finalSession}
	applyUsage(&res, finalUsage)
	return res, info, nil
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
