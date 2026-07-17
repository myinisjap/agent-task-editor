package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
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
// classifyCodexJSON parser.
//
// MCP servers are configured via $CODEX_HOME/config.toml's [mcp_servers.*]
// TOML sections (confirmed via `codex mcp add` against a scratch CODEX_HOME).
// Because that's a persistent config file the CLI reads on startup rather
// than a per-invocation flag, this runner points CODEX_HOME at a fresh
// per-run temp directory (so concurrent runs on the same host never
// share/clobber a global config) and writes a minimal config.toml into it.
type CodexRunner struct {
	// Path to the codex binary. Defaults to "codex" (resolved via PATH).
	BinaryPath string
	MCP        *MCPManager
	// UploadDir is the server-side directory where task attachments are stored.
	// Reserved for future use; codex exec's -i/--image flag could attach these
	// directly once wired up.
	UploadDir string
	// BackendURL / APIToken let the create_subtask MCP tool post children live to
	// the backend REST API (same container). Set from server config.
	BackendURL string
	APIToken   string
}

func (r *CodexRunner) binary() string {
	if r.BinaryPath != "" {
		return r.BinaryPath
	}
	return "codex"
}

// codexRunConfig carries the per-run CODEX_HOME directory prepared for MCP
// server wiring, so Run can clean it up after the process exits.
type codexRunConfig struct {
	HomeDir string
}

// prepareCodexHome creates a per-run CODEX_HOME directory containing a
// config.toml with a single [mcp_servers.task-editor] section, so the codex
// CLI picks it up without touching any shared/global config on the host.
// Returns nil if there is no MCP server to configure (entry is nil).
func prepareCodexHome(runID string, entry *mcpServerEntry) (*codexRunConfig, error) {
	if entry == nil {
		return nil, nil
	}
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("ate-codex-home-%s", runID))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("mkdir codex home: %w", err)
	}
	toml := renderCodexMCPTOML("task-editor", *entry)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0600); err != nil {
		return nil, fmt.Errorf("write codex config.toml: %w", err)
	}
	return &codexRunConfig{HomeDir: dir}, nil
}

func (c *codexRunConfig) Cleanup() {
	if c == nil {
		return
	}
	_ = os.RemoveAll(c.HomeDir)
}

// renderCodexMCPTOML renders a single [mcp_servers.<name>] TOML section for
// the given server entry, matching the shape `codex mcp add` writes (verified
// against a live `codex mcp add` invocation):
//
//	[mcp_servers.<name>]
//	command = "..."
//	args = ["...", ...]
//
//	[mcp_servers.<name>.env]
//	KEY = "..."
//
// Values are escaped with strconv.Quote, which produces valid TOML
// basic-string escaping for the ASCII-only command/args/env values this
// codebase generates (our own mcp-server binary path plus JSON-encoded
// string env vars — never arbitrary untrusted TOML-breaking input).
func renderCodexMCPTOML(name string, entry mcpServerEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[mcp_servers.%s]\n", name)
	fmt.Fprintf(&b, "command = %s\n", strconv.Quote(entry.Command))
	if len(entry.Args) > 0 {
		args := make([]string, len(entry.Args))
		for i, a := range entry.Args {
			args[i] = strconv.Quote(a)
		}
		fmt.Fprintf(&b, "args = [%s]\n", strings.Join(args, ", "))
	} else {
		b.WriteString("args = []\n")
	}
	if len(entry.Env) > 0 {
		fmt.Fprintf(&b, "\n[mcp_servers.%s.env]\n", name)
		for k, v := range entry.Env {
			fmt.Fprintf(&b, "%s = %s\n", k, strconv.Quote(v))
		}
	}
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
//     yolo / gemini's --yolo. This is a strictly stronger bypass than either
//     of those (it also disables the sandbox), which is the tradeoff for
//     fully non-interactive operation — see docs/providers/codex_cli.md for
//     the discussion of Codex's native sandbox/approval-mode system and how
//     command_allowlist/command_denylist map onto it (they don't: neither is
//     enforced for this provider today, a documented gap).
//   - --skip-git-repo-check allows running outside a bare git checkout the
//     CLI recognizes (worktrees can trip this check).
//   - There is no confirmed --max-turns-equivalent flag for `codex exec`, so
//     no turn cap is passed (see docs/providers/codex_cli.md).
func buildCodexArgs(input RunInput) []string {
	args := []string{
		"exec",
		"--json",
		"--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox",
	}
	if input.AgentConfig.Model != "" {
		args = append(args, "--model", input.AgentConfig.Model)
	}
	// codex resumes via a `resume <id>` subcommand inserted after `exec`, not an
	// appendable --resume flag like every other provider. Flags above still apply.
	if input.ResumeSessionID != "" {
		args = append(args, "resume", input.ResumeSessionID)
	}
	args = append(args, buildPrompt(input)+"\n\n"+buildSystemPrompt(input))
	return args
}

func (r *CodexRunner) Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error) {
	// Set up MCP sidecar if manager is configured. Unlike claude/qwen (which
	// take a per-invocation --mcp-config JSON file), the codex CLI only reads
	// MCP servers from $CODEX_HOME/config.toml, so a fresh CODEX_HOME is
	// prepared per run — but the sidecar's result-file protocol (RUN_ID/
	// RESULT_FILE/etc. env vars) is identical, so MCPManager.Prepare is still
	// used to produce the sidecar's env vars and result file.
	var mcpCfg *MCPRunConfig
	var codexHome *codexRunConfig
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
			return Result{Status: "failed"}, fmt.Errorf("prepare mcp: %w", err)
		}
		defer mcpCfg.Cleanup()

		entry := mcpServerEntry{
			Command: r.MCP.ServerBinary,
			Args:    []string{},
			Env:     mcpSidecarEnv(input, r.BackendURL, r.APIToken),
		}
		codexHome, err = prepareCodexHome(input.RunID, &entry)
		if err != nil {
			return Result{Status: "failed"}, fmt.Errorf("prepare codex home: %w", err)
		}
		defer codexHome.Cleanup()
	}

	args := buildCodexArgs(input)

	timeoutSecs := input.AgentConfig.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 600
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.binary(), args...)
	cmd.Dir = input.RepoPath
	env := mergeEnv(os.Environ(), input.AgentConfig.Env)
	if codexHome != nil {
		env = append(env, "CODEX_HOME="+codexHome.HomeDir)
	}
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{Status: "failed"}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{Status: "failed"}, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return Result{Status: "failed"}, fmt.Errorf("start codex: %w", err)
	}

	logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("started codex pid=%d", cmd.Process.Pid), At: time.Now()}

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

	// Stream stdout (JSONL events) — Codex's own event schema. Note: codex
	// exec --json can interleave non-JSON diagnostic lines (e.g. "ERROR
	// codex_api::... failed to connect") on stdout alongside the JSONL
	// events; classifyCodexJSON falls back to a raw LogStdout entry (still
	// scanned by ClassifyLine below) for any line that doesn't parse as JSON.
	go func() {
		defer wg.Done()
		rawDump := openRawDump(input.RunID) // dev-only; see rawDump in claude.go
		defer rawDump.Close()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			rawDump.WriteLine(line)
			entry, parsed, u, class, sid := classifyCodexJSON(line)
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
			if class == ClassNone {
				class = ClassifyLine(line)
			}
			switch class {
			case ClassRateLimit:
				mu.Lock()
				rateLimited = true
				mu.Unlock()
			case ClassTransient:
				mu.Lock()
				transient = true
				mu.Unlock()
			}
			if u != nil {
				mu.Lock()
				usage = u
				mu.Unlock()
			}
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			logCh <- LogEntry{Type: LogStderr, Content: line, At: time.Now()}
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
		logCh <- LogEntry{Type: LogSystem, Content: "agent timed out", At: time.Now()}
		return Result{Status: "failed"}, &ErrTransient{Cause: fmt.Errorf("codex run timed out")}
	}
	if err != nil {
		logCh <- LogEntry{Type: LogSystem, Content: fmt.Sprintf("codex exited: %v", err), At: time.Now()}
		mu.Lock()
		rl := rateLimited
		tr := transient
		mu.Unlock()
		if rl {
			return Result{Status: "failed"}, &ErrRateLimit{Message: "codex CLI 429: Request rejected by API rate limit"}
		}
		if tr {
			return Result{Status: "failed"}, &ErrTransient{Cause: fmt.Errorf("codex CLI exited with transient infra error: %w", err)}
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
		applyUsage(&res, finalUsage)
		res.SessionID = finalSession
		if err != nil && res.Outcome == "" {
			failed := Result{Status: "failed", SessionID: finalSession}
			applyUsage(&failed, finalUsage)
			return failed, nil
		}
		return res, nil
	}

	if err != nil && outcome == "" {
		failed := Result{Status: "failed", SessionID: finalSession}
		applyUsage(&failed, finalUsage)
		return failed, nil
	}
	res := Result{Status: "completed", Outcome: outcome, SessionID: finalSession}
	applyUsage(&res, finalUsage)
	return res, nil
}

// classifyCodexJSON parses one JSONL event from `codex exec --json`. Codex's
// event schema (confirmed against codex-rs/exec/src/exec_events.rs upstream
// and a live invocation) is:
//
//	{"type":"thread.started","thread_id":"..."}
//	{"type":"turn.started"}
//	{"type":"turn.completed","usage":{"input_tokens":...,"cached_input_tokens":...,"output_tokens":...,"reasoning_output_tokens":...}}
//	{"type":"turn.failed","error":{"message":"..."}}
//	{"type":"item.started","item":{"id":"...","type":"agent_message"|"reasoning"|"command_execution"|"file_change"|"mcp_tool_call"|"web_search"|"todo_list"|"error",...}}
//	{"type":"item.updated","item":{...}}
//	{"type":"item.completed","item":{...}}
//	{"type":"error","message":"..."}
//
// This is a completely different vendor/schema from claude/qwen/gemini's
// stream formats, so it is parsed independently.
//
// Returns the log entry, an optional outcome ("success"/"failure") parsed
// from an OUTCOME marker in the final agent_message item's text (Codex's
// JSON has no separate free-text summary field, so the terminal
// agent_message item.completed is scanned the same way claude/qwen scan
// their "result" message text), token usage (non-nil only for
// "turn.completed", which carries a cost-free token count — no total-cost
// figure is reported by Codex's JSON output, so CostUSD is left at zero, not
// estimated), a failure Classification derived from "turn.failed"/"error"
// events, and the session/thread_id carried on "thread.started".
func classifyCodexJSON(line string) (LogEntry, string, *runUsage, Classification, string) {
	var envelope struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Usage    struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string             `json:"message"`
		Item    *codexItemEnvelope `json:"item"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		// Codex interleaves plain-text diagnostic lines (e.g. Rust `tracing`
		// ERROR logs) with the JSONL event stream on stdout; these aren't
		// events, just raw log noise worth keeping for debuggability.
		return LogEntry{Type: LogStdout, Content: line, At: time.Now()}, "", nil, ClassNone, ""
	}

	switch envelope.Type {
	case "thread.started":
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, "", nil, ClassNone, envelope.ThreadID
	case "turn.started":
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, "", nil, ClassNone, ""
	case "turn.completed":
		usage := &runUsage{InputTokens: envelope.Usage.InputTokens, OutputTokens: envelope.Usage.OutputTokens}
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, "", usage, ClassNone, ""
	case "turn.failed":
		msg := ""
		if envelope.Error != nil {
			msg = envelope.Error.Message
		}
		class := ClassifyLine(msg)
		if class == ClassNone {
			class = ClassGenuine
		}
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, "failure", nil, class, ""
	case "item.started", "item.updated":
		return classifyCodexItem(envelope.Item, line, false)
	case "item.completed":
		return classifyCodexItem(envelope.Item, line, true)
	case "error":
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, "", nil, ClassifyLine(envelope.Message), ""
	default:
		return LogEntry{Type: LogStdout, Content: line, At: time.Now()}, "", nil, ClassNone, ""
	}
}

// codexItemEnvelope is the shape of the "item" field on item.started/
// item.updated/item.completed events (see ThreadItem/ThreadItemDetails in
// codex-rs/exec/src/exec_events.rs).
type codexItemEnvelope struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// AgentMessage / Reasoning
	Text string `json:"text"`
	// CommandExecution
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	Status           string `json:"status"`
	// McpToolCall
	Server string `json:"server"`
	Tool   string `json:"tool"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// classifyCodexItem renders a single thread item (agent_message, reasoning,
// command_execution, mcp_tool_call, file_change, web_search, todo_list,
// error) as a LogEntry, mapping tool-shaped items (command_execution,
// mcp_tool_call) to LogToolCall/LogToolResult and everything else to
// LogStdout/LogSystem. completed indicates this is a terminal item.completed
// event (vs. item.started/item.updated, which are progress notifications).
func classifyCodexItem(item *codexItemEnvelope, line string, completed bool) (LogEntry, string, *runUsage, Classification, string) {
	if item == nil {
		return LogEntry{Type: LogStdout, Content: line, At: time.Now()}, "", nil, ClassNone, ""
	}

	switch item.Type {
	case "agent_message":
		outcome := ""
		if completed {
			outcome = extractOutcome(item.Text)
		}
		return LogEntry{Type: LogStdout, Content: item.Text, At: time.Now()}, outcome, nil, ClassNone, ""
	case "reasoning":
		return LogEntry{Type: LogStdout, Content: item.Text, At: time.Now()}, "", nil, ClassNone, ""
	case "command_execution":
		if !completed {
			return LogEntry{Type: LogToolCall, Content: line, At: time.Now()}, "", nil, ClassNone, ""
		}
		class := ClassNone
		if item.Status == "failed" {
			class = ClassifyLine(item.AggregatedOutput)
		}
		return LogEntry{Type: LogToolResult, Content: line, At: time.Now()}, "", nil, class, ""
	case "mcp_tool_call":
		if !completed {
			return LogEntry{Type: LogToolCall, Content: line, At: time.Now()}, "", nil, ClassNone, ""
		}
		class := ClassNone
		if item.Status == "failed" && item.Error != nil {
			class = ClassifyLine(item.Error.Message)
		}
		return LogEntry{Type: LogToolResult, Content: line, At: time.Now()}, "", nil, class, ""
	case "file_change", "web_search", "todo_list":
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, "", nil, ClassNone, ""
	case "error":
		return LogEntry{Type: LogSystem, Content: line, At: time.Now()}, "", nil, ClassifyLine(item.Text), ""
	default:
		return LogEntry{Type: LogStdout, Content: line, At: time.Now()}, "", nil, ClassNone, ""
	}
}
