package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"
)

// OpencodeRunner runs the opencode CLI in headless mode (run --format json).
// ponytail: MCP not supported — opencode has no --mcp-config flag; configure MCP globally via opencode config
type OpencodeRunner struct {
	BinaryPath string
}

func (r *OpencodeRunner) binary() string {
	if r.BinaryPath != "" {
		return r.BinaryPath
	}
	return "opencode"
}

func (r *OpencodeRunner) Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error) {
	args := []string{"run", "--format", "json"}
	if input.AgentConfig.Model != "" {
		args = append(args, "-m", input.AgentConfig.Model)
	}
	// ponytail: opencode has no --mcp-config flag; MCP tools unavailable for opencode runs
	// "--" stops yargs flag parsing; prompt may contain "--" sequences that would be misread as flags
	args = append(args, "--", buildPrompt(input))

	timeoutSecs := input.AgentConfig.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 600
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	slog.Info("opencode args", "args", args)
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
		return Result{Status: "failed"}, fmt.Errorf("start opencode: %w", err)
	}

	logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("started opencode pid=%d", cmd.Process.Pid), At: time.Now()}

	var (
		wg      sync.WaitGroup
		outcome string
		mu      sync.Mutex
	)
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			entry, parsed := classifyOpencodeJSON(line)
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
		logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("opencode exited: %v", err), At: time.Now()}
	}

	return Result{Status: "completed", Outcome: outcome}, nil
}

// classifyOpencodeJSON parses one NDJSON line from opencode run --format json.
//
// Known gap: opencode's `run --format json` NDJSON output (text/tool_use/
// tool_result/step_finish/step_start message types) does not currently
// include a token usage or cost field in any of the shapes we've observed,
// so InputTokens/OutputTokens/CostUSD are left at zero (not estimated) for
// this provider rather than guessing. If a future opencode version adds
// usage reporting to one of these message types, wire it up the same way
// claude.go's classifyStreamJSON does for the "result" message.
func classifyOpencodeJSON(line string) (LogEntry, string) {
	var raw struct {
		Type string `json:"type"`
		Part struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Reason string `json:"reason"`
		} `json:"part"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return LogEntry{Type: LogStdout, Content: line, At: time.Now()}, ""
	}

	switch raw.Type {
	case "text":
		outcome := extractOutcome(raw.Part.Text)
		return LogEntry{Type: LogStdout, Content: raw.Part.Text, At: time.Now()}, outcome
	case "tool_use":
		return LogEntry{Type: LogToolCall, Content: line, At: time.Now()}, ""
	case "tool_result":
		return LogEntry{Type: LogToolResult, Content: line, At: time.Now()}, ""
	case "step_finish":
		// step_finish with reason="stop" means the agent is done
		if raw.Part.Reason == "stop" {
			return LogEntry{Type: LogSystem, Content: "step finished", At: time.Now()}, ""
		}
		return LogEntry{Type: LogSystem, Content: fmt.Sprintf("step finished: %s", raw.Part.Reason), At: time.Now()}, ""
	case "step_start":
		return LogEntry{Type: LogSystem, Content: "step started", At: time.Now()}, ""
	default:
		// Log unknown types as raw stdout for debuggability
		text := raw.Part.Text
		if text == "" {
			text = line
		}
		return LogEntry{Type: LogStdout, Content: text, At: time.Now()}, ""
	}
}
