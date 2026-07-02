// Package agent implements the agent runtime: provider interface, bounded pool,
// dispatcher, and concrete backends (ClaudeRunner, LLMRunner, QwenRunner).
package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// QwenRunner runs the Qwen Code CLI in headless mode (-p + stream-json).
// Qwen accepts the same {"mcpServers":{...}} config and mcp__<server>__<tool>
// tool naming as the Claude CLI, so it reuses MCPManager and the stream-json
// parsers from claude.go.
type QwenRunner struct {
	// Path to the qwen binary. Defaults to "qwen" (resolved via PATH).
	BinaryPath string
	MCP        *MCPManager
	// UploadDir is the server-side directory where task attachments are stored.
	// Reserved for future use if Qwen CLI gains an --image flag.
	UploadDir string
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
// --max-turns default/override behavior) can be unit tested without
// spawning a subprocess — mirrors buildClaudeArgs in claude.go.
func buildQwenArgs(input RunInput, mcpCfg *MCPRunConfig) []string {
	maxTurns := input.AgentConfig.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 50
	}

	args := []string{
		"-p", buildPrompt(input),
		"--system-prompt", buildSystemPrompt(input),
		"--output-format", "stream-json",
		"--approval-mode", "yolo",
		"--max-turns", strconv.FormatInt(maxTurns, 10),
	}
	if input.AgentConfig.Model != "" {
		args = append(args, "--model", input.AgentConfig.Model)
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
		)
	}
	return args
}

func (r *QwenRunner) Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error) {
	// Set up MCP sidecar if manager is configured.
	var mcpCfg *MCPRunConfig
	if r.MCP != nil && r.MCP.ServerBinary != "" {
		var err error
		mcpCfg, err = r.MCP.Prepare(input.RunID, input.Transitions, nil)
		if err != nil {
			return Result{Status: "failed"}, fmt.Errorf("prepare mcp: %w", err)
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
		return Result{Status: "failed"}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{Status: "failed"}, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return Result{Status: "failed"}, fmt.Errorf("start qwen: %w", err)
	}

	logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("started qwen pid=%d", cmd.Process.Pid), At: time.Now()}

	var (
		wg      sync.WaitGroup
		outcome string
		mu      sync.Mutex
	)
	wg.Add(2)

	// Stream stdout (stream-json lines) — same envelope as the claude CLI.
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
		logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("qwen exited: %v", err), At: time.Now()}
	}

	// MCP result takes priority; fall back to OUTCOME text parsing if the
	// agent completed without calling signal_complete.
	if mcpCfg != nil {
		res := mcpCfg.ReadResult()
		if res.Outcome == "" && outcome != "" {
			res.Outcome = outcome
		}
		// Non-zero exit with no signalled outcome means the subprocess crashed
		// before signal_complete. ReadResult defaults to "completed", which would
		// mask the failure and re-dispatch forever. Trust the exit code.
		if err != nil && res.Outcome == "" {
			return Result{Status: "failed"}, nil
		}
		return res, nil
	}

	// Non-zero exit with no parsed outcome means the agent failed.
	if err != nil && outcome == "" {
		return Result{Status: "failed"}, nil
	}
	return Result{Status: "completed", Outcome: outcome}, nil
}
