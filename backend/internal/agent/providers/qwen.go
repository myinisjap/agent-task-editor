// Package providers implements the concrete agent backends (ClaudeRunner,
// QwenRunner, CodexRunner, OpencodeRunner) and the MCP sidecar manager.
package providers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/agentuser"
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
	// Unused by this runner: images are attached from the worktree copies the
	// dispatcher makes (see qwenImageAtRefs), referenced from the prompt text
	// via @path tokens — qwen has no --image flag (verified against 0.25.2's
	// --help output; see docs/providers/qwen_code.md § Image Attachments).
	UploadDir string
	// BackendURL lets the create_subtask MCP tool post children live to the
	// backend REST API (same container). Set from server config. The bearer
	// token is a per-run scoped one minted in MCPManager.Prepare.
	BackendURL string
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
		"-p", buildQwenPrompt(input),
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

	// Command denylist: qwen v0.25.2 exposes --exclude-tools, which feeds its
	// getPermissionsDeny() policy and is honored even under --approval-mode
	// yolo (verified by reading the shipped 0.25.2 bundle: the shell-command
	// confirmation path calls doesToolInvocationMatch("run_shell_command",
	// invocation, excludeTools) before auto-approving, regardless of
	// approval mode).
	//
	// IMPORTANT (verified against the 0.25.2 bundle, not a live authenticated
	// run — see docs/providers/qwen_code.md): qwen's shell tool is named
	// run_shell_command (legacy class name ShellTool), NOT Bash. The
	// Bash(pattern) convention used for claude's --allowedTools/
	// --disallowedTools (and for qwen's own --allowed-tools above, which is
	// a bypass-confirmation list, not an enforcement list, so the wrong tool
	// name there is harmless) is a complete no-op on this deny path: neither
	// doesToolInvocationMatch's bare-name branch nor its "ToolName(arg)"
	// branch ever matches a candidate list that only contains "Bash(...)"
	// entries, since the invoked tool's name is always run_shell_command.
	// Each pattern is therefore emitted as "run_shell_command(pattern)"
	// instead.
	//
	// qwen's pattern matching is also not a glob: doesToolInvocationMatch
	// matches an invocation's command string argument by exact equality or
	// by the pattern being a literal whitespace-delimited *prefix* of the
	// command ("argValue===argPattern || argValue.startsWith(argPattern+"
	// ")") — a literal "*" in the pattern matches nothing (it's compared
	// character-for-character, never expanded). Patterns written in the
	// shared cross-provider "*"-wildcard convention (e.g. "git *", matching
	// claude's Bash(pattern) glob shape) are therefore normalized via
	// qwenDenylistArg below: a trailing "*" (optionally preceded by
	// whitespace) is stripped so "git *" becomes the prefix "git", which
	// correctly prefix-matches "git clone ..." etc. under qwen's own
	// semantics. A bare pattern with no trailing "*" is passed through
	// as-is and only matches that exact command string.
	for _, pat := range input.AgentConfig.CommandDenylist {
		if pat == "" {
			continue
		}
		args = append(args, "--exclude-tools", "run_shell_command("+qwenDenylistArg(pat)+")")
	}
	return args
}

// qwenDenylistArg converts a shared cross-provider "*"-wildcard glob pattern
// (as used by claude's command_denylist, e.g. "git *" or "rm -rf *") into the
// literal prefix qwen's doesToolInvocationMatch actually matches against: a
// trailing "*", and any whitespace immediately before it, is stripped, since
// qwen has no glob expansion and would otherwise never match the pattern
// (see the comment in buildQwenArgs above). A pattern with no trailing "*"
// is returned unchanged, matching only that exact command string.
func qwenDenylistArg(pattern string) string {
	trimmed := strings.TrimSuffix(pattern, "*")
	trimmed = strings.TrimRight(trimmed, " \t")
	if trimmed == "" {
		// A bare "*" (match everything) has no literal-prefix equivalent;
		// fall back to the original pattern rather than emitting an empty
		// "run_shell_command()" (which would only match an empty command).
		return pattern
	}
	return trimmed
}

// qwenImageAtRefs returns @-prefixed references, one per task image
// attachment, to append to the prompt text sent to qwen. qwen's headless -p
// mode resolves @<path> tokens via its at-command processor (handleAtCommand,
// called unconditionally from runNonInteractive in the 0.25.2 bundle) the
// same way the interactive CLI does: an image file is read, sniffed, and
// embedded as an inlineData content part (base64 + mimeType) IF the
// configured model's modalities declare image support — otherwise qwen
// itself skips the file with a logged warning and continues. There is no
// --image flag (confirmed against 0.25.2's --help output; unlike claude/
// codex, qwen has no CLI-level image flag at all), so this prompt-text route
// is the only way to attach images in headless mode.
//
// The dispatcher copies every attachment into <worktree>/.task_attachments/
// before the run (see copyAttachmentsToWorktree), and those copies — relative
// to input.RepoPath, the qwen subprocess's cwd — are what's referenced here,
// mirroring codexImagePaths in codex.go. An attachment whose copy is missing
// is skipped rather than referenced, since a dangling @path reference is
// surfaced to the model as a "file not found" style message in the
// processed query; the prompt's own ATTACHED IMAGES text list (written by
// buildPrompt) still names every attachment regardless.
func qwenImageAtRefs(input agent.RunInput) []string {
	var refs []string
	seen := map[string]bool{}
	for _, rel := range input.Task.Attachments {
		p := filepath.Join(".task_attachments", filepath.Base(rel))
		if seen[p] {
			continue
		}
		if fi, err := os.Stat(filepath.Join(input.RepoPath, p)); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		seen[p] = true
		refs = append(refs, "@"+p)
	}
	return refs
}

// buildQwenPrompt is buildPrompt's output (or, for a resumed session,
// buildResumePrompt's — see claude.go's identical --resume handling: a
// resumed session already contains the task context as prior conversation
// turns, so only the new information is sent as the next message) with an
// @-prefixed reference appended for each task image attachment, so qwen's
// at-command processor (see qwenImageAtRefs) actually attaches the image
// content instead of the agent only seeing the prompt's plain-text "ATTACHED
// IMAGES" listing of paths.
func buildQwenPrompt(input agent.RunInput) string {
	prompt := buildPrompt(input)
	if input.ResumeSessionID != "" {
		prompt = buildResumePrompt(input)
	}
	refs := qwenImageAtRefs(input)
	if len(refs) == 0 {
		return prompt
	}
	return prompt + "\n\n" + strings.Join(refs, " ")
}

// qwenAttemptInfo carries the signals runAttempt observed that Run needs to
// decide whether a failed --resume attempt should be retried as a cold
// start. Mirrors claude.go's attemptInfo (kept as its own type since qwen's
// runAttempt doesn't need claude's cost-watchdog-kill bookkeeping).
type qwenAttemptInfo struct {
	// sawStream is true once at least one well-formed stream-json line
	// arrived — evidence the CLI actually started a conversation.
	sawStream bool
	// resumeError is true when the output carried an explicit
	// session-not-found signal for the --resume target.
	resumeError bool
	// exitedWithError is true when the subprocess exited non-zero (for any
	// reason).
	exitedWithError bool
}

// qwenShouldFallBackToColdStart mirrors claude.go's
// shouldFallBackToColdStart: a resumed attempt failed in a way that points
// at the resume itself (an explicit session-not-found signal, or an error
// exit before any stream output).
func qwenShouldFallBackToColdStart(info qwenAttemptInfo) bool {
	if info.resumeError {
		return true
	}
	return info.exitedWithError && !info.sawStream
}

func (r *QwenRunner) Run(ctx context.Context, input agent.RunInput, logCh chan<- agent.LogEntry) (agent.Result, error) {
	res, info, err := r.runAttempt(ctx, input, logCh)
	if input.ResumeSessionID != "" && qwenShouldFallBackToColdStart(info) {
		// The --resume target most likely no longer exists (session expired,
		// CLI updated, state moved). Non-fatal: retry once from a cold start
		// with the full prompt, mirroring claude.go's identical fallback.
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("could not resume session %s — starting a fresh session", input.ResumeSessionID), At: time.Now()}
		input.ResumeSessionID = ""
		res, _, err = r.runAttempt(ctx, input, logCh)
	}
	return res, err
}

func (r *QwenRunner) runAttempt(ctx context.Context, input agent.RunInput, logCh chan<- agent.LogEntry) (agent.Result, qwenAttemptInfo, error) {
	var info qwenAttemptInfo
	// Set up MCP sidecar if manager is configured.
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
			return agent.Result{Status: "failed"}, info, fmt.Errorf("prepare mcp: %w", err)
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

	// QWEN_CODE_SUPPRESS_YOLO_WARNING keeps the headless yolo warning out of
	// stderr logs. NO_UPDATE_NOTIFIER disables the update-notifier package's
	// background version check (any value in env disables it — see
	// update-notifier's source) — same self-update-drift concern as claude's
	// DISABLE_AUTOUPDATER, since QWEN_CLI_VERSION is pinned in
	// backend/Dockerfile.
	env := mergeEnv(providerBaseEnv(qwenEnvAllowlist), input.AgentConfig.Env)
	if envOverridesKey(input.AgentConfig.Env, "OPENAI_BASE_URL") {
		// Custom endpoint from the provider config: don't send the backend's
		// own inherited OpenAI key there (a key the config supplies is kept).
		env = dropInheritedEnv(env, input.AgentConfig.Env, "OPENAI_API_KEY")
	}
	env = append(env, "QWEN_CODE_SUPPRESS_YOLO_WARNING=1", "NO_UPDATE_NOTIFIER=1")

	runBinary, runArgs, env, err := applyRuntime(input.Runtime, r.binary(), sanitizeArgs(args), env)
	if err != nil {
		return agent.Result{Status: "failed"}, info, err
	}
	cmd := newCLICommand(runCtx, runBinary, runArgs...)
	cmd.Dir = input.RepoPath
	cmd.Env = agentuser.Env(env) // HOME etc. of the agent account when isolated

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.Result{Status: "failed"}, info, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agent.Result{Status: "failed"}, info, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agent.Result{Status: "failed"}, info, fmt.Errorf("start qwen: %w", err)
	}

	logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("started qwen pid=%d", cmd.Process.Pid), At: time.Now()}

	var (
		wg                  sync.WaitGroup
		outcome             string
		sessionID           string
		rateLimited         bool
		transient           bool
		maxTurnsHit         bool
		usage               *runUsage
		cumInputTokens      int64
		cumOutputTokens     int64
		cumCacheReadTokens  int64
		cumCacheWriteTokens int64
		// seenMessageIDs dedupes assistant-message usage by message.id — see
		// claude.go's identical handling for why (a stream-json client can
		// split one API message into several events sharing the same
		// message.id, each repeating that message's full usage).
		seenMessageIDs map[string]bool
		// modelUsed is the model named on the stream-json "assistant"
		// messages (message.model) or the initial "system"/"init" event —
		// qwen's result event carries no model field, and AgentConfig.Model
		// may be left unset (the CLI's own default), so this is the only
		// structural source for pricing the run's final cost estimate. See
		// applyUsageWithCost below and docs/providers/qwen_code.md.
		modelUsed    string
		costWarned   bool
		costExceeded *agent.ErrCostBudgetExceeded
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
	// cost_watchdog.go. Inert when input.CostBudgetUSD is 0, or when no price
	// is known for the resolved model (qwen's own model IDs are not in the
	// pricing table today — a follow-up task is adding qwen-specific cost
	// estimation; until then this stays a correct no-op rather than a bogus
	// estimate).
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
				info.sawStream = true
				mu.Unlock()
			}
			if isResumeErrorLine(line) {
				mu.Lock()
				info.resumeError = true
				mu.Unlock()
			}
			if ev.Model != "" {
				mu.Lock()
				modelUsed = ev.Model
				mu.Unlock()
				// Watchdog-pricing fallback for an unset AgentConfig.Model —
				// see claude.go's identical handling.
				watchdog.setModelIfEmpty(ev.Model)
			}
			// Prefer the structured classification from the typed "result"
			// event. Raw-line sniffing (ClassifyLine) is reserved for lines
			// that never parsed as stream-json — a successfully-parsed event
			// has already been classified by the typed path, and re-sniffing
			// its Content (the agent's own prose/file contents) is pure
			// false-positive surface. See errclass.go and issue #335.
			class := ev.Class
			if class == agent.ClassNone && !ev.Parsed {
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
					// See claude.go's identical dedup handling: a stream-json
					// client can split one API message into several events
					// sharing the same message.id, each repeating that
					// message's full usage.
					count := true
					if ev.MessageID != "" {
						if seenMessageIDs == nil {
							seenMessageIDs = make(map[string]bool)
						}
						if seenMessageIDs[ev.MessageID] {
							count = false
						} else {
							seenMessageIDs[ev.MessageID] = true
						}
					}
					if count {
						cumInputTokens += ev.Usage.InputTokens
						cumOutputTokens += ev.Usage.OutputTokens
						cumCacheReadTokens += ev.Usage.CacheReadInputTokens
						cumCacheWriteTokens += ev.Usage.CacheCreationInputTokens
					}
					inTok, outTok, readTok, writeTok := cumInputTokens, cumOutputTokens, cumCacheReadTokens, cumCacheWriteTokens
					mu.Unlock()
					if watchdog.active() {
						if projected, warn, exceeded := watchdog.observe(context.Background(), inTok, readTok, writeTok, outTok); warn || exceeded {
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
									InputTokens:              inTok,
									OutputTokens:             outTok,
									CacheReadInputTokens:     readTok,
									CacheCreationInputTokens: writeTok,
									CostUSD:                  projected - input.CostSpentUSD,
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
			if isResumeErrorLine(line) {
				mu.Lock()
				info.resumeError = true
				mu.Unlock()
			}
			if is429Line(line) {
				mu.Lock()
				rateLimited = true
				mu.Unlock()
			} else if isMaxTurnsLine(line) {
				// qwen 0.25.2's handleMaxTurnsExceededError writes "Reached
				// max session turns for this session..." to stderr and
				// exits non-zero (FatalTurnLimitedError, exit code 53) —
				// unlike claude, it never emits a stream-json result with
				// subtype:"error_max_turns", so this is the only signal
				// available. See errclass.go and
				// docs/providers/qwen_code.md.
				mu.Lock()
				maxTurnsHit = true
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
	info.exitedWithError = err != nil

	mu.Lock()
	finalUsage := usage
	finalSession := sessionID
	mth := maxTurnsHit
	finalCostWarned := costWarned
	finalCostExceeded := costExceeded
	finalCostExceededUsage := costExceededUsage
	finalCumInputTokens := cumInputTokens
	finalCumOutputTokens := cumOutputTokens
	truncated := stdoutTruncated || stderrTruncated
	finalModel := modelUsed
	mu.Unlock()

	// qwen 0.25.2's actual max-turns behavior (handleMaxTurnsExceededError
	// printing to stderr and exiting non-zero — see errclass.go) never
	// reaches the terminal "result" event, so finalUsage is nil here even
	// though assistant messages (and their token usage) were emitted before
	// the cap was hit. Fall back to the cumulative per-turn usage tallied
	// from those assistant messages so a max-turns run doesn't silently
	// record cost_usd=0 despite having consumed tokens.
	if finalUsage == nil && mth && (finalCumInputTokens > 0 || finalCumOutputTokens > 0) {
		finalUsage = &runUsage{InputTokens: finalCumInputTokens, OutputTokens: finalCumOutputTokens}
	}

	// price applies usage/cost onto res, pricing via PriceResolver whenever
	// the result carried no authoritative total_cost_usd (qwen's own result
	// event never does — see docs/providers/qwen_code.md). model prefers the
	// configured model, falling back to what the CLI actually used
	// (finalModel, from the assistant stream-json messages) when the
	// provider config left Model unset.
	model := input.AgentConfig.Model
	if model == "" {
		model = finalModel
	}
	price := func(res *agent.Result, u *runUsage) {
		applyUsageWithCost(ctx, res, u, r.PriceResolver, model)
	}

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
		return res, info, finalCostExceeded
	}

	if truncated {
		// A single line exceeded the scan buffer limit — the rest of that
		// stream was dropped from the point the cap was hit. Surface it as a
		// visible warning rather than letting the run silently report
		// "completed" with missing output (see scan.go for detail).
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("qwen output stream truncated: a single line exceeded the %d-byte scan limit (bufio.ErrTooLong) — run output is incomplete", maxScanLineBytes), At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
		price(&res, finalUsage)
		return res, info, &agent.ErrTransient{Cause: fmt.Errorf("qwen output truncated at scan buffer limit")}
	}

	if err != nil && runCtx.Err() == context.DeadlineExceeded {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: "agent timed out", At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
		price(&res, finalUsage)
		return res, info, &agent.ErrTransient{Cause: fmt.Errorf("qwen run timed out")}
	}
	if err != nil {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("qwen exited: %v", err), At: time.Now()}
		mu.Lock()
		rl := rateLimited
		tr := transient
		mu.Unlock()
		if rl {
			res := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
			price(&res, finalUsage)
			return res, info, &agent.ErrRateLimit{Message: "qwen CLI 429: Request rejected by API rate limit"}
		}
		if tr {
			res := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
			price(&res, finalUsage)
			return res, info, &agent.ErrTransient{Cause: fmt.Errorf("qwen CLI exited with transient infra error: %w", err)}
		}
	}

	// Checked regardless of err: a run that hit max turns might exit 0 (if
	// qwen ever adopts claude's error_max_turns subtype behavior) or, as
	// qwen 0.25.2 actually does, exit non-zero with FatalTurnLimitedError
	// after printing the stderr line matched above — either way the
	// MCP/outcome fallthrough below must not get a chance to report this as
	// a normal "completed"/"failed" result, or the turn cap would silently
	// have no effect (re-dispatch would hand the next run a fresh turn
	// budget instead of escalating to waiting_human).
	if mth {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: "qwen hit its configured max-turns limit", At: time.Now()}
		res := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
		price(&res, finalUsage)
		configuredMaxTurns := input.AgentConfig.MaxTurns
		if configuredMaxTurns <= 0 {
			configuredMaxTurns = 50
		}
		return res, info, &agent.ErrMaxTurns{MaxTurns: int(configuredMaxTurns)}
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
		price(&res, finalUsage)
		res.SessionID = finalSession
		res.CostWarned = finalCostWarned
		// Any non-zero exit from the qwen binary means something went wrong
		// (e.g. auth error, crash, bad config). Even if a signal_complete outcome
		// was recorded, a non-zero exit overrides it — the agent may have signalled
		// before crashing, or the exit code may reflect an internal SDK error.
		if err != nil {
			if res.Outcome != "" {
				logCh <- agent.LogEntry{Type: agent.LogSystem, Content: exitErrorOutcomeMsg("qwen", res.Outcome), At: time.Now()}
			}
			failed := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
			price(&failed, finalUsage)
			return failed, info, nil
		}
		return res, info, nil
	}

	// Non-zero exit means the agent failed regardless of any parsed outcome.
	if err != nil {
		if outcome != "" {
			logCh <- agent.LogEntry{Type: agent.LogSystem, Content: exitErrorOutcomeMsg("qwen", outcome), At: time.Now()}
		}
		failed := agent.Result{Status: "failed", SessionID: finalSession, CostWarned: finalCostWarned}
		price(&failed, finalUsage)
		return failed, info, nil
	}
	res := agent.Result{Status: "completed", Outcome: outcome, SessionID: finalSession, CostWarned: finalCostWarned}
	price(&res, finalUsage)
	return res, info, nil
}
