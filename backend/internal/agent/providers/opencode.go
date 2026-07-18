package providers

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
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

func (r *OpencodeRunner) Run(ctx context.Context, input agent.RunInput, logCh chan<- agent.LogEntry) (agent.Result, error) {
	args := []string{"run", "--format", "json"}
	if input.AgentConfig.Model != "" {
		args = append(args, "-m", input.AgentConfig.Model)
	}
	if input.ResumeSessionID != "" {
		args = append(args, "--session", input.ResumeSessionID)
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
		return agent.Result{Status: "failed"}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agent.Result{Status: "failed"}, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agent.Result{Status: "failed"}, fmt.Errorf("start opencode: %w", err)
	}

	logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("started opencode pid=%d", cmd.Process.Pid), At: time.Now()}

	var (
		wg          sync.WaitGroup
		outcome     string
		rateLimited bool
		transient   bool
		mu          sync.Mutex
	)
	wg.Add(2)

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
			entry, parsed := classifyOpencodeJSON(line)
			logCh <- entry
			if parsed != "" {
				mu.Lock()
				outcome = parsed
				mu.Unlock()
			}
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
		return agent.Result{Status: "failed"}, &agent.ErrTransient{Cause: fmt.Errorf("opencode run timed out")}
	}
	if err != nil {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("opencode exited: %v", err), At: time.Now()}
		mu.Lock()
		rl := rateLimited
		tr := transient
		mu.Unlock()
		if rl {
			return agent.Result{Status: "failed"}, &agent.ErrRateLimit{Message: "opencode CLI 429: Request rejected by API rate limit"}
		}
		if tr {
			return agent.Result{Status: "failed"}, &agent.ErrTransient{Cause: fmt.Errorf("opencode CLI exited with transient infra error: %w", err)}
		}
	}

	return agent.Result{Status: "completed", Outcome: outcome}, nil
}
