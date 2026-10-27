package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/agentuser"
)

// CodexRunner runs the Codex CLI (`codex exec --json`) in non-interactive
// mode. Verified against `codex`/`codex exec` v0.142.5's --help output, a
// live (unauthenticated) invocation, and the upstream JSONL event schema
// (codex-rs/exec/src/exec_events.rs) — see docs/providers/codex_cli.md for
// the full trace of what was confirmed.
//
// Codex's JSONL event stream ({"type":"thread.started"|"turn.started"|
// "turn.completed"|"turn.failed"|"item.started"|"item.updated"|
// "item.completed"|"error", ...}) is a different vendor/schema entirely from
// claude/qwen's stream-json envelope, so this runner uses its own
// classifyCodexJSON parser (parse_codex.go).
//
// MCP servers are normally configured via $CODEX_HOME/config.toml's
// [mcp_servers.*] sections; this runner instead passes the sidecar as `-c`
// config overrides on each invocation (see codexMCPConfigArgs), so concurrent
// runs never share or clobber a config file and the user's own CODEX_HOME
// (login, sessions) is left in place.
type CodexRunner struct {
	// Path to the codex binary. Defaults to "codex" (resolved via PATH).
	BinaryPath string
	MCP        *MCPManager
	// UploadDir is the server-side directory where task attachments are stored.
	// Unused by this runner: images are attached from the worktree copies the
	// dispatcher makes (see codexImagePaths).
	UploadDir string
	// BackendURL lets the create_subtask MCP tool post children live to the
	// backend REST API (same container). Set from server config. The bearer
	// token is a per-run scoped one minted in MCPManager.Prepare.
	BackendURL string
	// PriceResolver resolves model to USD-per-1M pricing so the tokens
	// captured from "turn.completed" events (see classifyCodexJSON) can be
	// priced into a Result.CostUSD — codex's own JSON output reports no
	// total-cost figure, unlike claude/qwen's terminal total_cost_usd. nil
	// falls back to the hardcoded pricing table (defaultPriceResolver). See
	// applyUsageWithCost and docs/providers/codex_cli.md.
	PriceResolver PriceResolver
}

func (r *CodexRunner) binary() string {
	if r.BinaryPath != "" {
		return r.BinaryPath
	}
	return "codex"
}

// codexMCPConfigArgs renders a stdio MCP server entry as `-c` config
// overrides (`mcp_servers.<name>.command=...`, `.args=[...]`, and one
// `.env.<KEY>=...` per env var, in sorted key order), the per-invocation
// equivalent of the [mcp_servers.<name>] section `codex mcp add` writes to
// config.toml (verified with `codex mcp list` against codex 0.163.0).
//
// Overrides are used instead of pointing CODEX_HOME at a per-run directory
// holding a config.toml: CODEX_HOME is also where codex keeps its login
// (auth.json from `codex login`) and its session rollouts, so a fresh
// CODEX_HOME made every ChatGPT-login run fail as "Not logged in" and lost the
// thread a later `codex exec resume` needs. Each env var gets its own `-c`
// argument so no single argv string grows past what one value needs.
func codexMCPConfigArgs(name string, entry mcpServerEntry) []string {
	prefix := "mcp_servers." + name + "."
	args := []string{"-c", prefix + "command=" + tomlString(entry.Command)}
	quoted := make([]string, len(entry.Args))
	for i, a := range entry.Args {
		quoted[i] = tomlString(a)
	}
	args = append(args, "-c", prefix+"args=["+strings.Join(quoted, ", ")+"]")
	keys := make([]string, 0, len(entry.Env))
	for k := range entry.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-c", prefix+"env."+k+"="+tomlString(entry.Env[k]))
	}
	return args
}

// tomlString renders s as a TOML basic string. Unlike strconv.Quote it only
// emits escapes TOML defines (\\, \", \b, \t, \n, \f, \r, \uXXXX), so
// arbitrary text (review comments, transitions JSON) can't produce an
// invalid value; other Unicode is kept as-is, which TOML allows.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// buildCodexArgs constructs the CLI argument list for `codex exec` given the
// run input and whether MCP is configured. Extracted as a standalone
// function so the arg-construction logic can be unit tested without
// spawning a subprocess — mirrors buildQwenArgs in qwen.go.
//
// Notes on flags (verified against `codex exec --help`, v0.142.5):
//   - `codex exec [PROMPT]` is the non-interactive/scriptable subcommand.
//   - --json emits JSONL events to stdout (see classifyCodexJSON).
//   - --dangerously-bypass-approvals-and-sandbox skips all confirmation
//     prompts and runs without a sandbox — required for a headless run
//     (Codex otherwise pauses for interactive approval on every command),
//     mirroring the "run fully unattended" intent of qwen's --approval-mode
//     yolo. This is a strictly stronger bypass than that (it also disables
//     the sandbox), which is the tradeoff for fully non-interactive
//     operation — see docs/providers/codex_cli.md for
//     the discussion of Codex's native sandbox/approval-mode system and how
//     command_allowlist/command_denylist map onto it (they don't: neither is
//     enforced for this provider today, a documented gap).
//   - --skip-git-repo-check allows running outside a bare git checkout the
//     CLI recognizes (worktrees can trip this check).
//   - There is no confirmed --max-turns-equivalent flag for `codex exec`, so
//     no turn cap is passed (see docs/providers/codex_cli.md).
//
// configArgs are extra `-c key=value` overrides (the MCP sidecar wiring from
// codexMCPConfigArgs), placed with the other exec-level flags.
func buildCodexArgs(input agent.RunInput, configArgs ...string) []string {
	args := []string{
		"exec",
		"--json",
		"--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox",
	}
	args = append(args, configArgs...)
	if input.AgentConfig.Model != "" {
		args = append(args, "--model", input.AgentConfig.Model)
	}
	if v, ok := codexReasoningEffort(input.AgentConfig.Effort); ok {
		// codex has no --effort flag; reasoning effort is a `-c` config
		// override. codex only accepts minimal|low|medium|high — xhigh/max
		// (which codex has no equivalent tier for) are clamped down to
		// "high" by codexReasoningEffort rather than passed through
		// unrecognized. See effort.go.
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", v))
	}
	// codex resumes via a `resume <id>` subcommand inserted after `exec`, not an
	// appendable --resume flag like every other provider. Flags above still apply.
	if input.ResumeSessionID != "" {
		args = append(args, "resume", input.ResumeSessionID)
	}
	// Images go after the (optional) resume subcommand: `codex exec resume`
	// has its own -i/--image flag for the prompt sent after resuming, and the
	// `exec`-level one only attaches to an initial prompt. The `--image=<path>`
	// form is required — -i/--image takes a variadic value list, so a bare
	// `--image a.png "<prompt>"` swallows the prompt as a second image and
	// codex falls back to reading the prompt from stdin.
	for _, img := range codexImagePaths(input) {
		args = append(args, "--image="+img)
	}
	args = append(args, buildPrompt(input)+"\n\n"+buildSystemPrompt(input))
	return args
}

// codexImagePaths returns the task's image attachments as paths relative to
// the run's working directory (input.RepoPath), for codex's --image flag.
// The dispatcher copies every attachment into <worktree>/.task_attachments/
// before the run (see copyAttachmentsToWorktree), and those copies are what's
// passed: unlike the server's upload dir, the worktree is readable by the
// isolated agent user codex runs as. An attachment whose copy is missing is
// skipped rather than passed through, since codex fails the whole run on an
// unreadable --image path; the prompt still lists every attachment.
func codexImagePaths(input agent.RunInput) []string {
	var paths []string
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
		paths = append(paths, p)
	}
	return paths
}

// codexMissingRolloutMarker is the error codex prints when `exec resume`
// names a thread it has no session file for (verified against 0.163.0:
// "thread/resume failed: no rollout found for thread id <id>").
const codexMissingRolloutMarker = "no rollout found for thread id"

func (r *CodexRunner) Run(ctx context.Context, input agent.RunInput, logCh chan<- agent.LogEntry) (agent.Result, error) {
	res, resumeMissing, err := r.runAttempt(ctx, input, logCh)
	if resumeMissing && input.ResumeSessionID != "" && ctx.Err() == nil {
		// The thread to resume is gone (e.g. its session was written to a
		// per-run CODEX_HOME by an older version of this runner, or pruned):
		// start a fresh session once instead of failing every retry the
		// same way, as the claude runner does.
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("codex has no saved session %q to resume; starting a fresh session", input.ResumeSessionID), At: time.Now()}
		input.ResumeSessionID = ""
		res, _, err = r.runAttempt(ctx, input, logCh)
	}
	return res, err
}

// runAttempt runs codex once. resumeMissing reports that codex rejected the
// resume because the thread's session file doesn't exist.
func (r *CodexRunner) runAttempt(ctx context.Context, input agent.RunInput, logCh chan<- agent.LogEntry) (agent.Result, bool, error) {
	var resumeMissing bool // set by the stderr scanner; read after wg.Wait()
	// Set up MCP sidecar if manager is configured. Unlike claude/qwen (which
	// take a per-invocation --mcp-config JSON file), codex reads MCP servers
	// from its config, so the sidecar is wired in through `-c` overrides (see
	// codexMCPConfigArgs) — but the sidecar's result-file protocol (RUN_ID/
	// RESULT_FILE/etc. env vars) is identical, so MCPManager.Prepare is still
	// used to produce the sidecar's env vars and result file.
	var mcpCfg *MCPRunConfig
	var configArgs []string
	if r.MCP != nil && r.MCP.ServerBinary != "" {
		var err error
		mcpCfg, err = r.MCP.Prepare(input.RunID, input.Transitions, input.OpenReviewComments, nil, &SubtaskEnv{
			BackendURL:  r.BackendURL,
			TaskID:      input.Task.ID,
			Enabled:     input.AgentConfig.SubtasksEnabled,
			MaxSubtasks: input.AgentConfig.MaxSubtasks,
		})
		if err != nil {
			return agent.Result{Status: "failed"}, resumeMissing, fmt.Errorf("prepare mcp: %w", err)
		}
		defer mcpCfg.Cleanup()

		configArgs = codexMCPConfigArgs("task-editor", mcpServerEntry{
			Command: r.MCP.ServerBinary,
			Args:    []string{},
			Env:     mcpSidecarEnv(input, r.BackendURL, mcpCfg.Token, mcpCfg.ResultFile),
		})
	}

	args := buildCodexArgs(input, configArgs...)

	timeoutSecs := input.AgentConfig.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 600
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()
	// procCtx is cancelled to stop codex at the max_turns cap; kept separate
	// from runCtx so a cap hit isn't mistaken for the run timeout.
	procCtx, stopProc := context.WithCancel(runCtx)
	defer stopProc()
	maxTurns := input.AgentConfig.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 50 // same default as the claude/qwen_code runners
	}

	env := mergeEnv(providerBaseEnv(codexEnvAllowlist), input.AgentConfig.Env)
	if envOverridesKey(input.AgentConfig.Env, "OPENAI_BASE_URL") {
		// Custom endpoint from the provider config: don't send the backend's
		// own inherited OpenAI key there (a key the config supplies is kept).
		env = dropInheritedEnv(env, input.AgentConfig.Env, "OPENAI_API_KEY")
	}

	runBinary, runArgs, env, err := applyRuntime(input.Runtime, r.binary(), sanitizeArgs(args), env)
	if err != nil {
		return agent.Result{Status: "failed"}, resumeMissing, err
	}
	cmd := newCLICommand(procCtx, runBinary, runArgs...)
	cmd.Dir = input.RepoPath
	cmd.Env = agentuser.Env(env) // HOME etc. of the agent account when isolated

	// Pricing inputs read back from codex's own session file (best-effort,
	// see codex_rollout.go): the session's cumulative usage before a resumed
	// run, and after the run the model codex actually used when the config
	// leaves it unset.
	codexHome := codexHomeDir(cmd.Env)
	var baseline *runUsage
	if input.ResumeSessionID != "" {
		baseline = readCodexRollout(findCodexRollout(codexHome, input.ResumeSessionID)).Total
	}
	var (
		rolloutOnce sync.Once
		rollout     codexRolloutInfo
	)
	price := func(res *agent.Result, u *runUsage, threadID string) {
		model := input.AgentConfig.Model
		if threadID == "" {
			threadID = input.ResumeSessionID
		}
		if threadID != "" && (model == "" || baseline != nil || u == nil) {
			// Read once per attempt: some exit paths price two results.
			rolloutOnce.Do(func() {
				rollout = readCodexRollout(findCodexRollout(codexHome, threadID))
				if model == "" && rollout.Model != "" {
					logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("codex used its default model %q (no model set on the provider config); pricing the run against it", rollout.Model), At: time.Now()}
				}
			})
		}
		if model == "" {
			model = rollout.Model
		}
		applyUsageWithCost(ctx, res, codexRunUsage(u, baseline, rollout.Total), r.PriceResolver, model)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.Result{Status: "failed"}, resumeMissing, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return agent.Result{Status: "failed"}, resumeMissing, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agent.Result{Status: "failed"}, resumeMissing, fmt.Errorf("start codex: %w", err)
	}

	logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("started codex pid=%d", cmd.Process.Pid), At: time.Now()}

	var (
		wg              sync.WaitGroup
		outcome         string
		sessionID       string
		rateLimited     bool
		transient       bool
		usage           *runUsage
		toolSteps       int64
		maxTurnsHit     bool
		stdoutTruncated bool
		stderrTruncated bool
		mu              sync.Mutex
	)
	wg.Add(2)

	// Stream stdout (JSONL events) — Codex's own event schema. Note: codex
	// exec --json can interleave non-JSON diagnostic lines (e.g. "ERROR
	// codex_api::... failed to connect") on stdout alongside the JSONL
	// events; classifyCodexJSON falls back to a raw LogStdout entry for any
	// line that doesn't parse as JSON — only those lines are still scanned
	// by ClassifyLine below (raw-line sniffing is pure false-positive
	// surface on the payload of a successfully-parsed event; see issue
	// #335).
	go func() {
		defer wg.Done()
		rawDump := openRawDump(input.RunID) // dev-only; see rawDump in cli.go
		defer rawDump.Close()
		scanErr := scanLines(stdout, func(line string) {
			if line == "" {
				return
			}
			rawDump.WriteLine(line)
			entry, parsed, u, class, sid, isJSON := classifyCodexJSON(line)
			logCh <- entry
			if isJSON && codexCompletedToolStep(line) {
				mu.Lock()
				toolSteps++
				if toolSteps > maxTurns && !maxTurnsHit {
					// Stop at the cap, like claude's --max-turns: the run
					// escalates to waiting_human instead of retrying.
					maxTurnsHit = true
					stopProc()
				}
				mu.Unlock()
			}
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
			if class == agent.ClassNone && !isJSON {
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
			if u != nil {
				mu.Lock()
				usage = u
				mu.Unlock()
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
			if input.ResumeSessionID != "" && strings.Contains(line, codexMissingRolloutMarker) {
				mu.Lock()
				resumeMissing = true
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
	truncated := stdoutTruncated || stderrTruncated
	hitCap := maxTurnsHit
	steps := toolSteps
	mu.Unlock()

	// price records usage on res and reports the tool steps as turns used.
	priceRun := func(res *agent.Result, u *runUsage, threadID string) {
		price(res, u, threadID)
		res.TurnsUsed = steps
	}

	if hitCap {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("codex reached max_turns (%d tool steps); stopping the run", maxTurns), At: time.Now()}
		mu.Lock()
		finalUsage := usage
		finalSession := sessionID
		mu.Unlock()
		res := agent.Result{Status: "failed", SessionID: finalSession}
		priceRun(&res, finalUsage, finalSession)
		return res, resumeMissing, &agent.ErrMaxTurns{MaxTurns: int(maxTurns)}
	}

	if truncated {
		// A single line exceeded the scan buffer limit — the rest of that
		// stream was dropped from the point the cap was hit. Surface it as a
		// visible warning rather than letting the run silently report
		// "completed" with missing output (see scan.go for detail).
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("codex output stream truncated: a single line exceeded the %d-byte scan limit (bufio.ErrTooLong) — run output is incomplete", maxScanLineBytes), At: time.Now()}
		mu.Lock()
		finalUsage := usage
		finalSession := sessionID
		mu.Unlock()
		res := agent.Result{Status: "failed", SessionID: finalSession}
		priceRun(&res, finalUsage, finalSession)
		return res, resumeMissing, &agent.ErrTransient{Cause: fmt.Errorf("codex output truncated at scan buffer limit")}
	}

	if err != nil && runCtx.Err() == context.DeadlineExceeded {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: "agent timed out", At: time.Now()}
		mu.Lock()
		finalUsage := usage
		finalSession := sessionID
		mu.Unlock()
		res := agent.Result{Status: "failed", SessionID: finalSession}
		priceRun(&res, finalUsage, finalSession)
		return res, resumeMissing, &agent.ErrTransient{Cause: fmt.Errorf("codex run timed out")}
	}
	if err != nil {
		logCh <- agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("codex exited: %v", err), At: time.Now()}
		mu.Lock()
		rl := rateLimited
		tr := transient
		finalUsage := usage
		finalSession := sessionID
		mu.Unlock()
		if rl {
			res := agent.Result{Status: "failed", SessionID: finalSession}
			priceRun(&res, finalUsage, finalSession)
			return res, resumeMissing, &agent.ErrRateLimit{Message: "codex CLI 429: Request rejected by API rate limit"}
		}
		if tr {
			res := agent.Result{Status: "failed", SessionID: finalSession}
			priceRun(&res, finalUsage, finalSession)
			return res, resumeMissing, &agent.ErrTransient{Cause: fmt.Errorf("codex CLI exited with transient infra error: %w", err)}
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
		priceRun(&res, finalUsage, finalSession)
		res.SessionID = finalSession
		// Any non-zero exit from the codex binary means something went wrong
		// (e.g. auth error, crash, bad config). Even if a signal_complete outcome
		// was recorded, a non-zero exit overrides it — the agent may have signalled
		// before crashing, or the exit code may reflect an internal SDK error.
		if err != nil {
			if res.Outcome != "" {
				logCh <- agent.LogEntry{Type: agent.LogSystem, Content: exitErrorOutcomeMsg("codex", res.Outcome), At: time.Now()}
			}
			failed := agent.Result{Status: "failed", SessionID: finalSession}
			priceRun(&failed, finalUsage, finalSession)
			return failed, resumeMissing, nil
		}
		return res, resumeMissing, nil
	}

	// Non-zero exit means the agent failed regardless of any parsed outcome.
	if err != nil {
		if outcome != "" {
			logCh <- agent.LogEntry{Type: agent.LogSystem, Content: exitErrorOutcomeMsg("codex", outcome), At: time.Now()}
		}
		failed := agent.Result{Status: "failed", SessionID: finalSession}
		priceRun(&failed, finalUsage, finalSession)
		return failed, resumeMissing, nil
	}
	res := agent.Result{Status: "completed", Outcome: outcome, SessionID: finalSession}
	priceRun(&res, finalUsage, finalSession)
	return res, resumeMissing, nil
}

// mcpSidecarEnv builds the env vars the mcp-server sidecar binary expects,
// identical to what MCPManager.Prepare configures for the claude/qwen
// --mcp-config entry — factored out here since CodexRunner needs to embed
// them directly into a config.toml [mcp_servers.*] entry rather than letting
// MCPManager write them into its own JSON file.
func mcpSidecarEnv(input agent.RunInput, backendURL, apiToken, resultFile string) map[string]string {
	transitionsJSON, _ := json.Marshal(input.Transitions)
	reviewCommentsJSON, _ := json.Marshal(input.OpenReviewComments)
	env := map[string]string{
		"RUN_ID":          input.RunID,
		"RESULT_FILE":     resultFile,
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
