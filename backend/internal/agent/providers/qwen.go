// Package providers implements the concrete agent backends (ClaudeRunner,
// AnthropicRunner, LLMRunner, QwenRunner, CodexRunner,
// OpencodeRunner) and the MCP sidecar manager.
package providers

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// QwenRunner runs the Qwen Code CLI in headless mode (-p + stream-json).
// Qwen accepts the same {"mcpServers":{...}} config and mcp__<server>__<tool>
// tool naming as the Claude CLI, so it reuses MCPManager and the stream-json
// parsers from parse_streamjson.go (via parse_qwen.go's classifyQwenJSON).
type QwenRunner struct {
	// Path to the qwen binary. Defaults to "qwen" (resolved via PATH).
	BinaryPath string
	MCP        *MCPManager
	// UploadDir is the server-side directory where task attachments are stored.
	// Reserved for future use if Qwen CLI gains an --image flag.
	UploadDir string
	// BackendURL / APIToken let the create_subtask MCP tool post children live to
	// the backend REST API (same container). Set from server config.
	BackendURL string
	APIToken   string
}

func (r *QwenRunner) binary() string {
	if r.BinaryPath != "" {
		return r.BinaryPath
	}
	return "qwen"
}

// buildQwenArgs constructs the CLI argument list for the qwen binary given
// the run input and (optional) prepared MCP config. Extracted as a
// standalone function so the arg-construction logic (in particular the
// --max-session-turns default/override behavior) can be unit tested without
// spawning a subprocess — mirrors buildClaudeArgs in claude.go.
func buildQwenArgs(input agent.RunInput, mcpCfg *MCPRunConfig) []string {
	maxTurns := input.AgentConfig.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 50
	}

	args := []string{
		"-p", buildPrompt(input),
		"--system-prompt", buildSystemPrompt(input),
		"--output-format", "stream-json",
		"--approval-mode", "yolo",
		"--max-session-turns", strconv.FormatInt(maxTurns, 10),
	}
	if input.AgentConfig.Model != "" {
		args = append(args, "--model", input.AgentConfig.Model)
	}
	if input.ResumeSessionID != "" {
		args = append(args, "--resume", input.ResumeSessionID)
	}
	if mcpCfg != nil {
		args = append(args, "--mcp-config", mcpCfg.ConfigFile)
		// qwen uses --allowed-tools (space/array) and the same mcp__ prefix as claude.
		args = append(args, "--allowed-tools",
			"mcp__task-editor__get_task_transitions",
			"mcp__task-editor__signal_complete",
			"mcp__task-editor__request_human",
			"mcp__task-editor__update_task_notes",
			"mcp__task-editor__store_info",
			"mcp__task-editor__resolve_comment",
		)
	}
	// Command allowlist: intentionally NOT wired to --allowed-tools. That
	// flag only bypasses the confirmation prompt for listed tools/patterns —
	// it does not restrict which commands run — and --approval-mode yolo
	// (above) auto-approves everything regardless, so an allowlist entry
	// (or its absence) has no enforcement effect either way. See
	// docs/providers/qwen_code.md for details. AgentConfig.CommandAllowlist
	// is therefore left unused here; the capability matrix records it as
	// unsupported so operators aren't misled.

	// Command denylist: qwen v0.21.0 exposes --exclude-tools, which feeds
	// its permissionsDeny policy and is honored even under yolo mode.
	// Mirror the Bash(pattern) convention used for --allowed-tools above
	// and elsewhere in the codebase (e.g. the claude provider).
	//
	// NOTE: the Bash(pattern) glob shape is confirmed for --allowed-tools
	// but has not been verified live for --exclude-tools specifically. If
	// qwen only accepts bare tool names on the deny path, per-pattern
	// denial may silently degrade to a no-op and a blanket
	// "--exclude-tools Bash" (no pattern) would be the safer fallback.
	for _, pat := range input.AgentConfig.CommandDenylist {
		if pat == "" {
			continue
		}
		args = append(args, "--exclude-tools", "Bash("+pat+")")
	}
	return args
}

func (r *QwenRunner) Run(ctx context.Context, input agent.RunInput, logCh chan<- agent.LogEntry) (agent.Result, error) {
	// Set up MCP sidecar if manager is configured.
	var mcpCfg *MCPRunConfig
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
			return agent.Result{Status: "failed"}, fmt.Errorf("prepare mcp: %w", err)
		}
		defer mcpCfg.Cleanup()
	}

	args := buildQwenArgs(input, mcpCfg)

	timeoutSecs := input.AgentConfig.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 600
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.binary(), args...)
	cmd.Dir = input.RepoPath
	// QWEN_CODE_SUPPRESS_YOLO_WARNING keeps the headless yolo warning out of stderr logs.
	cmd.Env = mergeEnv(os.Environ(), input.AgentConfig.Env)
	cmd.Env = append(cmd.Env, "QWEN_CODE_SUPPRESS_YOLO_WARNING=1")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.Result{Status: "failed"}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agent.Result{Status: "failed"}, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agent.Result{Status: "failed"}, fmt.Errorf("start qwen: %w", err)
	}

	logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("started qwen pid=%d", cmd.Process.Pid), At: time.Now()}

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

	// Stream stdout (stream-json lines) — same envelope as the claude CLI.
	go func() {
		defer wg.Done()
		rawDump := openRawDump(input.RunID) // dev-only; see rawDump in cli.go
		defer rawDump.Close()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			rawDump.WriteLine(line)
			ev := classifyQwenJSON(line)
			logCh <- ev.Entry
			if ev.Outcome != "" {
				mu.Lock()
				outcome = ev.Outcome
				mu.Unlock()
			}
			if ev.SessionID != "" {
				mu.Lock()
				sessionID = ev.SessionID
				mu.Unlock()
			}
			// Prefer the structured classification from the typed "result"
			// event; fall back to sniffing the raw line. See errclass.go.
			class := ev.Class
			if class == agent.ClassNone {
				class = agent.ClassifyLine(line)
			}
			switch class {
			case agent.ClassRateLimit:
				mu.Lock()
				rateLimited = true
				mu.Unlock()
			case agent.ClassTransient:
				mu.Lock()
				transient = true
				mu.Unlock()
			}
			if ev.Usage != nil {
				mu.Lock()
				usage = ev.Usage
				mu.Unlock()
			}
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			logCh <- agent.LogEntry{Type: agent.LogStderr, Content: line, At: time.Now()}
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
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: "agent timed out", At: time.Now()}
		return agent.Result{Status: "failed"}, &agent.ErrTransient{Cause: fmt.Errorf("qwen run timed out")}
	}
	if err != nil {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("qwen exited: %v", err), At: time.Now()}
		mu.Lock()
		rl := rateLimited
		tr := transient
		mu.Unlock()
		if rl {
			return agent.Result{Status: "failed"}, &agent.ErrRateLimit{Message: "qwen CLI 429: Request rejected by API rate limit"}
		}
		if tr {
			return agent.Result{Status: "failed"}, &agent.ErrTransient{Cause: fmt.Errorf("qwen CLI exited with transient infra error: %w", err)}
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
		// ReadResult() (the MCP sidecar result file) has no knowledge of
		// token usage/cost — that only comes from the CLI's stream-json
		// "result" message — so merge it in here.
		applyUsage(&res, finalUsage)
		res.SessionID = finalSession
		// Non-zero exit with no signalled outcome means the subprocess crashed
		// before signal_complete. ReadResult defaults to "completed", which would
		// mask the failure and re-dispatch forever. Trust the exit code.
		if err != nil && res.Outcome == "" {
			failed := agent.Result{Status: "failed", SessionID: finalSession}
			applyUsage(&failed, finalUsage)
			return failed, nil
		}
		return res, nil
	}

	// Non-zero exit with no parsed outcome means the agent failed.
	if err != nil && outcome == "" {
		failed := agent.Result{Status: "failed", SessionID: finalSession}
		applyUsage(&failed, finalUsage)
		return failed, nil
	}
	res := agent.Result{Status: "completed", Outcome: outcome, SessionID: finalSession}
	applyUsage(&res, finalUsage)
	return res, nil
}
