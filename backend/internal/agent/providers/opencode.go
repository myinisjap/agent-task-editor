package providers

import (
	"context"
	"fmt"
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

	env := mergeEnv(allowlistEnv(opencodeEnvAllowlist), input.AgentConfig.Env)
	runBinary, runArgs, env, err := applyRuntime(input.Runtime, r.binary(), sanitizeArgs(args), env)
	if err != nil {
		return agent.Result{Status: "failed"}, err
	}
	cmd := exec.CommandContext(runCtx, runBinary, runArgs...)
	cmd.Dir = input.RepoPath
	cmd.Env = env

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
		sessionID   string
		rateLimited bool
		transient   bool
		// usage holds the most recently observed step_finish usage. Per the
		// cumulative-to-date assumption documented on classifyOpencodeJSON,
		// each step_finish's cost/tokens are taken as-is (assign, not sum)
		// rather than accumulated across steps.
		usage           *runUsage
		stdoutTruncated bool
		stderrTruncated bool
		mu              sync.Mutex
	)
	wg.Add(2)

	go func() {
		defer wg.Done()
		rawDump := openRawDump(input.RunID) // dev-only; see rawDump in cli.go
		defer rawDump.Close()
		scanErr := scanLines(stdout, func(line string) {
			if line == "" {
				return
			}
			rawDump.WriteLine(line)
			entry, parsed, u, sid, parsedJSON := classifyOpencodeJSON(line)
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
			if u != nil {
				mu.Lock()
				usage = u
				mu.Unlock()
			}
			// opencode has no typed error classification (unlike
			// claude/qwen's Class or codex's Classification return), so the
			// raw-line sniff below is opencode's only rate-limit/transient
			// signal on stdout. Scope it to lines that never parsed as JSON:
			// a successfully-parsed event's Content is the agent's own
			// prose/tool output, and sniffing it is pure false-positive
			// surface (see issue #335). A rate limit reported only inside a
			// structured event body would no longer be caught here — accept
			// this; opencode CLI errors also surface on stderr (still
			// unconditionally sniffed below) and via non-zero exit.
			if !parsedJSON {
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
		})
		if scanErr != nil {
			mu.Lock()
			stdoutTruncated = true
			mu.Unlock()
		}
	}()

	go func() {
		defer wg.Done()
		scanErr := scanLines(stderr, func(line string) {
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
		})
		if scanErr != nil {
			mu.Lock()
			stderrTruncated = true
			mu.Unlock()
		}
	}()

	wg.Wait()
	err = cmd.Wait()

	mu.Lock()
	finalSession := sessionID
	finalUsage := usage
	truncated := stdoutTruncated || stderrTruncated
	mu.Unlock()

	if truncated {
		// A single line exceeded the scan buffer limit — the rest of that
		// stream was dropped from the point the cap was hit. Surface it as a
		// visible warning rather than letting the run silently report
		// "completed" with missing output (see scan.go for detail).
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("opencode output stream truncated: a single line exceeded the %d-byte scan limit (bufio.ErrTooLong) — run output is incomplete", maxScanLineBytes), At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession}
		applyUsage(&res, finalUsage)
		return res, &agent.ErrTransient{Cause: fmt.Errorf("opencode output truncated at scan buffer limit")}
	}

	if err != nil && runCtx.Err() == context.DeadlineExceeded {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: "agent timed out", At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession}
		applyUsage(&res, finalUsage)
		return res, &agent.ErrTransient{Cause: fmt.Errorf("opencode run timed out")}
	}
	if err != nil {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("opencode exited: %v", err), At: time.Now()}
		mu.Lock()
		rl := rateLimited
		tr := transient
		mu.Unlock()
		if rl {
			res := agent.Result{Status: "failed", SessionID: finalSession}
			applyUsage(&res, finalUsage)
			return res, &agent.ErrRateLimit{Message: "opencode CLI 429: Request rejected by API rate limit"}
		}
		if tr {
			res := agent.Result{Status: "failed", SessionID: finalSession}
			applyUsage(&res, finalUsage)
			return res, &agent.ErrTransient{Cause: fmt.Errorf("opencode CLI exited with transient infra error: %w", err)}
		}
		// Any remaining non-zero exit (not rate-limit/transient) means the
		// agent failed, regardless of whether an OUTCOME marker was parsed.
		// Originally this only overrode a "completed" result when no OUTCOME
		// was found (see TestOpencodeRunner_Run_Exit1NoOutputIsFailed), which
		// left a crash-after-signal window: if the CLI emitted OUTCOME=success
		// and then crashed (e.g. mid-teardown panic, auth expiry after the
		// final turn), the run was persisted as "completed" and the task moved
		// forward on an unverified result. Every other CLI provider now applies
		// this same override (claude's stricter rule, adopted for parity).
		if outcome != "" {
			logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("opencode exited with error but had outcome %q — treating as failed", outcome), At: time.Now()}
		}
		// Usage/cost must still be persisted here even though the run
		// failed — money may have been spent before the crash (mirrors
		// qwen.go's equivalent failure path).
		res := agent.Result{Status: "failed", SessionID: finalSession}
		applyUsage(&res, finalUsage)
		return res, nil
	}

	res := agent.Result{Status: "completed", Outcome: outcome, SessionID: finalSession}
	applyUsage(&res, finalUsage)
	return res, nil
}
