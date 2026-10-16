package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/agentuser"
)

// OpencodeRunner runs the opencode CLI in headless mode (run --format json).
//
// Unlike claude/qwen (a per-invocation --mcp-config file) or codex (-c
// config overrides), opencode has no CLI flag for MCP servers or a system
// prompt at all; both are delivered via OPENCODE_CONFIG_CONTENT (see
// opencodeConfigContent). There is deliberately no PriceResolver field here
// (unlike QwenRunner/CodexRunner): opencode's step_finish cost is
// authoritative and the mid-run cost watchdog must not be added to this
// provider (see providers/AGENTS.md and ADR 0013) — its per-step cost is
// only a *delta*, not a running total, so it can't be projected mid-run.
type OpencodeRunner struct {
	BinaryPath string
	MCP        *MCPManager
	// UploadDir is the server-side directory where task attachments are
	// stored. Reserved for future use if opencode CLI gains an image flag.
	UploadDir string
	// BackendURL lets the create_subtask MCP tool post children live to the
	// backend REST API (same container). Set from server config. The bearer
	// token is a per-run scoped one minted in MCPManager.Prepare.
	BackendURL string
}

func (r *OpencodeRunner) binary() string {
	if r.BinaryPath != "" {
		return r.BinaryPath
	}
	return "opencode"
}

// opencodeDefaultAgent is the agent `opencode run` uses when the CLI isn't
// told otherwise (verified against opencode-ai v1.19.1).
const opencodeDefaultAgent = "build"

// opencodeMaxStepsMessage is the text opencode prints (verified against
// opencode-ai v1.19.1's agent loop) when agent.<name>.steps is reached.
// classifyOpencodeJSON has no typed classification for this (opencode
// doesn't emit a distinct event type for it), so Run sniffs stdout/stderr
// lines for this substring as the config-side max-turns signal, backed up
// by a runner-side step_finish counter (see Run) in case this text changes
// or the config key is ignored by the installed opencode version.
const opencodeMaxStepsMessage = "maximum steps for this agent have been reached"

// opencodeConfigContent builds the JSON value for OPENCODE_CONFIG_CONTENT.
// OPENCODE_CONFIG_CONTENT (supported in opencode-ai v1.19.1) lets the CLI
// read its config from an env var instead of (or in addition to) an
// opencode.json file on disk, and is the only way to deliver several things
// `opencode run` has no flag for:
//
//   - The system prompt, set on the agent.<name>.prompt field (opencode run
//     has no --system-prompt/-p flag like claude/qwen). Without this,
//     buildSystemPrompt(input) — including the instruction to end with
//     "OUTCOME: success"/"OUTCOME: failure", the fallback used when the MCP
//     tool call doesn't land — would never reach the CLI at all.
//   - agent.<name>.steps, the per-agent turn cap ("maxSteps" is deprecated
//     in favor of "steps" as of opencode-ai v1.19.1). maxSteps is also
//     unknown by this implementation; only "steps" is emitted.
//   - mcp, the inline MCP server config (opencode has no --mcp-config flag
//     like claude/qwen). Only emitted when mcpCfg is non-nil. The
//     "task-editor" entry's command/environment are built from the exact
//     same env map used for the file-based MCPManager config (see
//     mcpSidecarEnv in codex.go, reused here), so it carries the sidecar's
//     scoped token / RESULT_FILE handoff per ADR 0004. Tool naming on the
//     opencode side has NOT been verified live (no opencode binary
//     available while writing this); buildSystemPrompt's mcp__task-editor__*
//     names are passed through unchanged, and the text OUTCOME: fallback
//     above is kept specifically as the safety net if that naming is wrong.
//   - permission, mapping command_denylist/command_allowlist onto opencode's
//     bash permission policy. opencode run auto-rejects any "ask"
//     permission unless --auto is passed (deliberately never passed here),
//     so a "deny" entry here genuinely blocks the pattern instead of
//     pausing for approval that will never come.
func opencodeConfigContent(systemPrompt string, maxSteps int64, mcpCfg *MCPRunConfig, serverBinary string, allowlist, denylist []string) (string, error) {
	agentCfg := map[string]any{
		"prompt": systemPrompt,
	}
	if maxSteps > 0 {
		agentCfg["steps"] = maxSteps
	}
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"agent": map[string]any{
			opencodeDefaultAgent: agentCfg,
		},
	}

	if mcpCfg != nil && serverBinary != "" {
		cfg["mcp"] = map[string]any{
			"task-editor": map[string]any{
				"type":        "local",
				"command":     []string{serverBinary},
				"enabled":     true,
				"environment": mcpCfg.Env,
			},
		}
	}

	if perm := opencodeBashPermission(allowlist, denylist); perm != nil {
		cfg["permission"] = map[string]any{"bash": perm}
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// opencodeBashPermission builds the "bash" permission map for opencode's
// `permission` config block from a command allowlist/denylist, or nil if
// both are empty. denylist entries always map to "deny"; allowlist entries
// map to "allow" plus a catch-all "*": "deny" default (so an allowlist is a
// true allowlist rather than merely adding exceptions) — but a pattern
// present in both wins as "deny", since the denylist is the stronger signal
// and must never be silently overridden by an allow entry for the same
// pattern. Empty-string patterns are skipped (mirrors qwen.go's
// buildQwenArgs denylist handling).
//
// opencode's bash permission pattern-matching semantics (most-specific-wins
// vs. first-match-wins, and whether "*" as a plain map key is even treated
// as a wildcard default rather than a literal command named "*") have not
// been verified live — no opencode binary was available while writing this.
// This mirrors the same kind of unverified-but-documented gap as qwen.go's
// --exclude-tools pattern shape (see docs/providers/qwen_code.md).
func opencodeBashPermission(allowlist, denylist []string) map[string]string {
	perm := map[string]string{}
	for _, pat := range denylist {
		if pat == "" {
			continue
		}
		perm[pat] = "deny"
	}
	if len(allowlist) > 0 {
		for _, pat := range allowlist {
			if pat == "" {
				continue
			}
			if _, denied := perm[pat]; denied {
				continue
			}
			perm[pat] = "allow"
		}
		if _, ok := perm["*"]; !ok {
			perm["*"] = "deny"
		}
	}
	if len(perm) == 0 {
		return nil
	}
	return perm
}

func (r *OpencodeRunner) Run(ctx context.Context, input agent.RunInput, logCh chan<- agent.LogEntry) (agent.Result, error) {
	// Unlike claude/qwen (a per-invocation --mcp-config file) or codex (-c
	// config overrides), opencode has no CLI flag for MCP servers; the
	// sidecar is instead wired in through OPENCODE_CONFIG_CONTENT's inline
	// "mcp" block (see opencodeConfigContent) — but the sidecar's
	// result-file protocol (RUN_ID/RESULT_FILE/etc. env vars) is identical,
	// so MCPManager.Prepare is still used to produce them.
	var mcpCfg *MCPRunConfig
	if r.MCP != nil && r.MCP.ServerBinary != "" {
		var err error
		mcpCfg, err = r.MCP.Prepare(input.RunID, input.Transitions, input.OpenReviewComments, nil, &SubtaskEnv{
			BackendURL:  r.BackendURL,
			TaskID:      input.Task.ID,
			Enabled:     input.AgentConfig.SubtasksEnabled,
			MaxSubtasks: input.AgentConfig.MaxSubtasks,
		})
		if err != nil {
			return agent.Result{Status: "failed"}, fmt.Errorf("prepare mcp: %w", err)
		}
		defer mcpCfg.Cleanup()
	}

	args := []string{"run", "--format", "json"}
	if input.AgentConfig.Model != "" {
		args = append(args, "-m", input.AgentConfig.Model)
	}
	if input.ResumeSessionID != "" {
		args = append(args, "--session", input.ResumeSessionID)
	}
	// "--" stops yargs flag parsing; prompt may contain "--" sequences that would be misread as flags
	args = append(args, "--", buildPrompt(input))

	timeoutSecs := input.AgentConfig.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 600
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()
	// procCtx is cancelled to stop opencode at the max_turns cap; kept
	// separate from runCtx so a cap hit isn't mistaken for the run timeout
	// (mirrors codex.go's procCtx/stopProc).
	procCtx, stopProc := context.WithCancel(runCtx)
	defer stopProc()
	maxTurns := input.AgentConfig.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 50 // same default as the claude/qwen_code/codex runners
	}

	serverBinary := ""
	if r.MCP != nil {
		serverBinary = r.MCP.ServerBinary
	}

	env := mergeEnv(providerBaseEnv(opencodeEnvAllowlist), input.AgentConfig.Env)
	// Delivered via the runner, not opencodeEnvAllowlist: OPENCODE_CONFIG_CONTENT
	// isn't inherited from the backend's own environment, it's generated per
	// run from buildSystemPrompt(input), the max_turns cap, the MCP sidecar,
	// and the command allowlist/denylist — see opencodeConfigContent.
	// Appended after mergeEnv so it always wins over any
	// OPENCODE_CONFIG_CONTENT an operator set on the agent config's own env
	// (os/exec.Cmd.Env keeps the last occurrence of a duplicate key): letting
	// that silently win would drop the OUTCOME instruction, the task tools,
	// the turn cap, and the command policy with no indication why.
	configContent, err := opencodeConfigContent(buildSystemPrompt(input), maxTurns, mcpCfg, serverBinary, input.AgentConfig.CommandAllowlist, input.AgentConfig.CommandDenylist)
	if err != nil {
		return agent.Result{Status: "failed"}, fmt.Errorf("build opencode config: %w", err)
	}
	env = append(env, "OPENCODE_CONFIG_CONTENT="+configContent)
	// Force-disabled: the CLI self-updating mid-run inside a container we
	// version and rebuild ourselves (OPENCODE_CLI_VERSION in
	// backend/Dockerfile) would silently drift the running binary away from
	// what's pinned. Mirrors claude's DISABLE_AUTOUPDATER and qwen's
	// NO_UPDATE_NOTIFIER above. Appended last so it always wins over any
	// operator-set OPENCODE_DISABLE_AUTOUPDATE in the backend's own env.
	env = append(env, "OPENCODE_DISABLE_AUTOUPDATE=1")
	runBinary, runArgs, env, err := applyRuntime(input.Runtime, r.binary(), sanitizeArgs(args), env)
	if err != nil {
		return agent.Result{Status: "failed"}, err
	}
	cmd := newCLICommand(procCtx, runBinary, runArgs...)
	cmd.Dir = input.RepoPath
	cmd.Env = agentuser.Env(env) // HOME etc. of the agent account when isolated

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
		// usage accumulates every step_finish's cost/tokens seen this run.
		// step_finish fires once per *step*, not once per run, and its
		// cost/tokens are per-step deltas (verified against opencode-ai
		// v1.19.1's session processor: assistantMessage.cost += D.cost per
		// step) — summed here rather than taking the last one, which would
		// undercount a multi-step run down to its final step alone. See
		// classifyOpencodeJSON's doc comment.
		usage           *runUsage
		stepCount       int64
		maxTurnsHit     bool
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
				usage = sumRunUsage(usage, u)
				mu.Unlock()
			}
			if parsedJSON && opencodeIsStepFinish(line) {
				mu.Lock()
				stepCount++
				if stepCount > maxTurns && !maxTurnsHit {
					// Runner-side backstop for agent.<name>.steps (see
					// opencodeConfigContent): stop at the cap like claude's
					// --max-turns even if the installed opencode version
					// ignores the config key. The config-side detection
					// below (opencodeMaxStepsMessage) also sets
					// maxTurnsHit when opencode itself stops first.
					maxTurnsHit = true
					stopProc()
				}
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
				if strings.Contains(line, opencodeMaxStepsMessage) {
					mu.Lock()
					if !maxTurnsHit {
						maxTurnsHit = true
						stopProc()
					}
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
			if strings.Contains(line, opencodeMaxStepsMessage) {
				mu.Lock()
				if !maxTurnsHit {
					maxTurnsHit = true
					stopProc()
				}
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
	hitCap := maxTurnsHit
	steps := stepCount
	mu.Unlock()

	// priceRun records usage on res and reports the step_finish count as
	// turns used (mirrors codex.go's priceRun closure).
	priceRun := func(res *agent.Result) {
		applyUsage(res, finalUsage)
		res.TurnsUsed = steps
	}

	if hitCap {
		// Checked first, like codex.go: a cap hit can arrive either via the
		// config-side "maximum steps" message (process may then exit 0) or
		// via the runner-side stepCount backstop (stopProc kills the
		// process, so err below would otherwise look like an unrelated
		// non-zero exit). Either way this must escalate to ErrMaxTurns
		// rather than fall through to the generic error/outcome handling.
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("opencode reached max_turns (%d steps); stopping the run", maxTurns), At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession}
		priceRun(&res)
		return res, &agent.ErrMaxTurns{MaxTurns: int(maxTurns)}
	}

	if truncated {
		// A single line exceeded the scan buffer limit — the rest of that
		// stream was dropped from the point the cap was hit. Surface it as a
		// visible warning rather than letting the run silently report
		// "completed" with missing output (see scan.go for detail).
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("opencode output stream truncated: a single line exceeded the %d-byte scan limit (bufio.ErrTooLong) — run output is incomplete", maxScanLineBytes), At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession}
		priceRun(&res)
		return res, &agent.ErrTransient{Cause: fmt.Errorf("opencode output truncated at scan buffer limit")}
	}

	if err != nil && runCtx.Err() == context.DeadlineExceeded {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: "agent timed out", At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession}
		priceRun(&res)
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
			priceRun(&res)
			return res, &agent.ErrRateLimit{Message: "opencode CLI 429: Request rejected by API rate limit"}
		}
		if tr {
			res := agent.Result{Status: "failed", SessionID: finalSession}
			priceRun(&res)
			return res, &agent.ErrTransient{Cause: fmt.Errorf("opencode CLI exited with transient infra error: %w", err)}
		}
	}

	// MCP result takes priority; fall back to OUTCOME text parsing if the
	// agent completed without calling signal_complete (mirrors qwen.go's
	// equivalent block).
	if mcpCfg != nil {
		res := mcpCfg.ReadResult()
		if res.Outcome == "" && outcome != "" {
			res.Outcome = outcome
		}
		// ReadResult() (the MCP sidecar result file) has no knowledge of
		// token usage/cost — that only comes from step_finish events — so
		// merge it in here.
		priceRun(&res)
		res.SessionID = finalSession
		// Any non-zero exit from the opencode binary means something went
		// wrong (e.g. auth error, crash, bad config). Even if a
		// signal_complete outcome was recorded, a non-zero exit overrides it
		// — the agent may have signalled before crashing, or the exit code
		// may reflect an internal CLI error (mirrors qwen.go/claude.go).
		if err != nil {
			if res.Outcome != "" {
				logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("opencode exited with error but had outcome %q — treating as failed", res.Outcome), At: time.Now()}
			}
			failed := agent.Result{Status: "failed", SessionID: finalSession}
			priceRun(&failed)
			return failed, nil
		}
		return res, nil
	}

	if err != nil {
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
		priceRun(&res)
		return res, nil
	}

	res := agent.Result{Status: "completed", Outcome: outcome, SessionID: finalSession}
	priceRun(&res)
	return res, nil
}
