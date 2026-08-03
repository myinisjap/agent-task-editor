// Package providers implements the concrete agent backends (ClaudeRunner,
// AnthropicRunner, LLMRunner, QwenRunner, CodexRunner,
// OpencodeRunner) and the MCP sidecar manager.
package providers

import (
	"context"
	"fmt"
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
	// PriceResolver resolves model to USD-per-1M pricing for the mid-run cost
	// watchdog (see cost_watchdog.go). Like claude, qwen's own terminal
	// total_cost_usd (when present) is used as-is for the final persisted
	// Result.CostUSD — this resolver is only consulted to *project* cost from
	// incremental token usage while the run is still in flight. nil falls
	// back to the hardcoded pricing table (defaultPriceResolver). The
	// watchdog is a no-op if the configured model isn't in the pricing table
	// (known=false) — see docs/providers/qwen_code.md.
	PriceResolver PriceResolver
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
		if input.AgentConfig.SubtasksEnabled {
			args = append(args, "--allowed-tools", "mcp__task-editor__create_subtask")
		}
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

	cmd := exec.CommandContext(runCtx, r.binary(), sanitizeArgs(args)...)
	cmd.Dir = input.RepoPath
	// QWEN_CODE_SUPPRESS_YOLO_WARNING keeps the headless yolo warning out of stderr logs.
	cmd.Env = mergeEnv(allowlistEnv(qwenEnvAllowlist), input.AgentConfig.Env)
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
		wg              sync.WaitGroup
		outcome         string
		sessionID       string
		rateLimited     bool
		transient       bool
		maxTurnsHit     bool
		usage           *runUsage
		cumInputTokens  int64
		cumOutputTokens int64
		costWarned      bool
		costExceeded    *agent.ErrCostBudgetExceeded
		// costExceededUsage snapshots this run's own cumulative token usage
		// and incremental cost (projected minus the watchdog's prior-spend
		// baseline) at the moment the watchdog cancels the subprocess. A
		// killed run never reaches its terminal "result" event, so this is
		// the only usage/cost data available to persist — see claude.go's
		// attemptInfo.costExceededUsage for the full rationale.
		costExceededUsage *runUsage
		stdoutTruncated   bool
		stderrTruncated   bool
		mu                sync.Mutex
	)
	wg.Add(2)

	// Mid-run cost watchdog — same mechanism as claude.go's runAttempt. See
	// cost_watchdog.go. Inert when input.CostBudgetUSD is 0.
	watchdog := newCostWatchdog(input.CostBudgetUSD, input.CostSpentUSD, input.CostWarnRatio, r.PriceResolver, input.AgentConfig.Model)

	// Stream stdout (stream-json lines) — same envelope as the claude CLI.
	go func() {
		defer wg.Done()
		rawDump := openRawDump(input.RunID) // dev-only; see rawDump in cli.go
		defer rawDump.Close()
		scanErr := scanLines(stdout, func(line string) {
			if line == "" {
				return
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
			case agent.ClassMaxTurns:
				mu.Lock()
				maxTurnsHit = true
				mu.Unlock()
			}
			if ev.Usage != nil {
				if ev.IsResult {
					mu.Lock()
					usage = ev.Usage
					mu.Unlock()
				} else {
					mu.Lock()
					cumInputTokens += ev.Usage.InputTokens
					cumOutputTokens += ev.Usage.OutputTokens
					inTok, outTok := cumInputTokens, cumOutputTokens
					mu.Unlock()
					if watchdog.active() {
						if projected, warn, exceeded := watchdog.observe(context.Background(), inTok, outTok); warn || exceeded {
							if warn {
								mu.Lock()
								costWarned = true
								mu.Unlock()
								logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("cost watchdog: projected run cost ($%.2f) has crossed the warning threshold (budget $%.2f)", projected, input.CostBudgetUSD), At: time.Now()}
							}
							if exceeded {
								mu.Lock()
								costExceeded = &agent.ErrCostBudgetExceeded{SpentUSD: projected, BudgetUSD: input.CostBudgetUSD}
								// This run's own incremental cost is the
								// projection minus the watchdog's prior-spend
								// baseline (input.CostSpentUSD) — projected
								// includes cost already recorded by earlier
								// runs, which must not be double-counted here.
								costExceededUsage = &runUsage{
									InputTokens:  inTok,
									OutputTokens: outTok,
									CostUSD:      projected - input.CostSpentUSD,
								}
								mu.Unlock()
								logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("cost watchdog: projected run cost ($%.2f) has crossed the budget ($%.2f) — cancelling run", projected, input.CostBudgetUSD), At: time.Now()}
								cancel()
							}
						}
					}
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
	finalUsage := usage
	finalSession := sessionID
	mth := maxTurnsHit
	finalCostWarned := costWarned
	finalCostExceeded := costExceeded
	finalCostExceededUsage := costExceededUsage
	truncated := stdoutTruncated || stderrTruncated
	mu.Unlock()

	if finalCostExceeded != nil {
		// The watchdog already cancelled the subprocess; err here is just
		// the resulting context-cancelled exit. Escalate to waiting_human
		// rather than classifying as transient/rate-limit.
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: "qwen run stopped: mid-run cost budget exceeded", At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
		// A killed run never reaches its terminal "result" event, so
		// finalUsage is always nil here — persist the watchdog's own
		// cumulative-usage/incremental-cost snapshot instead, or the run
		// would record cost_usd=0 despite having genuinely spent money.
		applyUsage(&res, finalCostExceededUsage)
		return res, finalCostExceeded
	}

	if truncated {
		// A single line exceeded the scan buffer limit — the rest of that
		// stream was dropped from the point the cap was hit. Surface it as a
		// visible warning rather than letting the run silently report
		// "completed" with missing output (see scan.go for detail).
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("qwen output stream truncated: a single line exceeded the %d-byte scan limit (bufio.ErrTooLong) — run output is incomplete", maxScanLineBytes), At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
		applyUsage(&res, finalUsage)
		return res, &agent.ErrTransient{Cause: fmt.Errorf("qwen output truncated at scan buffer limit")}
	}

	if err != nil && runCtx.Err() == context.DeadlineExceeded {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: "agent timed out", At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
		applyUsage(&res, finalUsage)
		return res, &agent.ErrTransient{Cause: fmt.Errorf("qwen run timed out")}
	}
	if err != nil {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("qwen exited: %v", err), At: time.Now()}
		mu.Lock()
		rl := rateLimited
		tr := transient
		mu.Unlock()
		if rl {
			res := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
			applyUsage(&res, finalUsage)
			return res, &agent.ErrRateLimit{Message: "qwen CLI 429: Request rejected by API rate limit"}
		}
		if tr {
			res := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
			applyUsage(&res, finalUsage)
			return res, &agent.ErrTransient{Cause: fmt.Errorf("qwen CLI exited with transient infra error: %w", err)}
		}
	}

	// qwen may exit 0 on the max-session-turns subtype (mirrors claude's
	// error_max_turns behavior), so this must be checked regardless of err —
	// otherwise the MCP/outcome fallthrough below would report the run as a
	// normal "completed" result and the turn cap would silently have no
	// effect.
	if mth {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: "qwen hit its configured max-turns limit", At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
		applyUsage(&res, finalUsage)
		configuredMaxTurns := input.AgentConfig.MaxTurns
		if configuredMaxTurns <= 0 {
			configuredMaxTurns = 50
		}
		return res, &agent.ErrMaxTurns{MaxTurns: int(configuredMaxTurns)}
	}

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
		res.CostWarned = finalCostWarned
		// Any non-zero exit from the qwen binary means something went wrong
		// (e.g. auth error, crash, bad config). Even if a signal_complete outcome
		// was recorded, a non-zero exit overrides it — the agent may have signalled
		// before crashing, or the exit code may reflect an internal SDK error.
		if err != nil {
			if res.Outcome != "" {
				logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("qwen exited with error but had outcome %q — treating as failed", res.Outcome), At: time.Now()}
			}
			failed := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
			applyUsage(&failed, finalUsage)
			return failed, nil
		}
		return res, nil
	}

	// Non-zero exit means the agent failed regardless of any parsed outcome.
	if err != nil {
		if outcome != "" {
			logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("qwen exited with error but had parsed outcome %q — treating as failed", outcome), At: time.Now()}
		}
		failed := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
		applyUsage(&failed, finalUsage)
		return failed, nil
	}
	res := agent.Result{Status: "completed", Outcome: outcome, SessionID: finalSession, CostWarned: finalCostWarned}
	applyUsage(&res, finalUsage)
	return res, nil
}
