package providers

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// TestBuildQwenArgs_Resume verifies ResumeSessionID is passed via --resume,
// and omitted when empty.
func TestBuildQwenArgs_Resume(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{Task: agent.Task{Title: "t"}, AgentConfig: agent.AgentConfig{}, ResumeSessionID: "s1"}, nil)
	if got := findFlagValue(args, "--resume"); got != "s1" {
		t.Fatalf("expected --resume s1, got %q (args=%v)", got, args)
	}
	none := buildQwenArgs(agent.RunInput{Task: agent.Task{Title: "t"}, AgentConfig: agent.AgentConfig{}}, nil)
	if findFlagValue(none, "--resume") != "" {
		t.Fatalf("did not expect --resume when ResumeSessionID empty, args=%v", none)
	}
}

// TestBuildQwenArgs_MaxTurnsDefault verifies that when AgentConfig.MaxTurns
// is unset (zero), the constructed args default --max-session-turns to 50
// (matching the claude provider's fallback behavior).
func TestBuildQwenArgs_MaxTurnsDefault(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
	}, nil)
	if got := findFlagValue(args, "--max-session-turns"); got != "50" {
		t.Fatalf("expected default --max-session-turns 50, got %q (args=%v)", got, args)
	}
}

// TestBuildQwenArgs_MaxTurnsConfigured verifies that a custom
// AgentConfig.MaxTurns value is passed through to the --max-session-turns
// flag instead of the hardcoded default.
func TestBuildQwenArgs_MaxTurnsConfigured(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{MaxTurns: 7},
	}, nil)
	if got := findFlagValue(args, "--max-session-turns"); got != "7" {
		t.Fatalf("expected --max-session-turns 7, got %q (args=%v)", got, args)
	}
}

// countFlagOccurrences counts how many times flag appears in args followed
// by exactly value (used since --allowed-tools may be repeated).
func countFlagOccurrences(args []string, flag, value string) int {
	n := 0
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			n++
		}
	}
	return n
}

// TestBuildQwenArgs_CommandAllowlist_NotWired verifies that CommandAllowlist
// is intentionally NOT translated into --allowed-tools entries (that flag
// only bypasses confirmation and is moot under --approval-mode yolo; see
// qwen.go and docs/providers/qwen_code.md).
func TestBuildQwenArgs_CommandAllowlist_NotWired(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task: agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{
			CommandAllowlist: []string{"git *", "npm test"},
		},
	}, nil)
	for i, a := range args {
		if a == "--allowed-tools" {
			t.Fatalf("expected no --allowed-tools flags from CommandAllowlist (unwired), found one at index %d in args=%v", i, args)
		}
	}
}

// TestBuildQwenArgs_CommandDenylist verifies that each CommandDenylist
// pattern is appended as a Bash(pattern) entry to --exclude-tools.
func TestBuildQwenArgs_CommandDenylist(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task: agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{
			CommandDenylist: []string{"git *", "npm test"},
		},
	}, nil)
	if countFlagOccurrences(args, "--exclude-tools", "Bash(git *)") != 1 {
		t.Fatalf("expected one --exclude-tools Bash(git *) entry, got args=%v", args)
	}
	if countFlagOccurrences(args, "--exclude-tools", "Bash(npm test)") != 1 {
		t.Fatalf("expected one --exclude-tools Bash(npm test) entry, got args=%v", args)
	}
}

// TestBuildQwenArgs_CommandDenylist_BlankPatternSkipped verifies that blank
// entries in CommandDenylist are skipped (mirrors the allowlist guard).
func TestBuildQwenArgs_CommandDenylist_BlankPatternSkipped(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task: agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{
			CommandDenylist: []string{"", "git *", ""},
		},
	}, nil)
	if countFlagOccurrences(args, "--exclude-tools", "Bash()") != 0 {
		t.Fatalf("expected blank denylist pattern to be skipped, got args=%v", args)
	}
	if countFlagOccurrences(args, "--exclude-tools", "Bash(git *)") != 1 {
		t.Fatalf("expected one --exclude-tools Bash(git *) entry, got args=%v", args)
	}
	n := 0
	for _, a := range args {
		if a == "--exclude-tools" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly one --exclude-tools flag total, got %d in args=%v", n, args)
	}
}

// TestBuildQwenArgs_NoCommandFilters_NoExtraFlags verifies that with no
// allowlist/denylist and no mcpCfg, neither --allowed-tools nor
// --exclude-tools appear.
func TestBuildQwenArgs_NoCommandFilters_NoExtraFlags(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
	}, nil)
	for i, a := range args {
		if a == "--allowed-tools" || a == "--exclude-tools" {
			t.Fatalf("expected no --allowed-tools/--exclude-tools flags without mcpCfg/filters, found %q at index %d in args=%v", a, i, args)
		}
	}
}

// TestQwenRunner_ErrorMaxTurns verifies that a subtype:"error_max_turns"
// stream-json result — which qwen, like claude, may emit with exit code 0 —
// drives Run to return *agent.ErrMaxTurns rather than reporting a normal
// "completed" result. Reuses the claude_test.go TestMain subprocess-helper
// pattern via a separate QWEN_TEST_HELPER env var.
func TestQwenRunner_ErrorMaxTurns(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)

	input := agent.RunInput{
		RunID: "qwen-test-run",
		Task:  agent.Task{ID: "task-1", Title: "test task"},
		AgentConfig: agent.AgentConfig{
			Env:         map[string]string{"QWEN_TEST_HELPER": "error_max_turns"},
			TimeoutSecs: 10,
			MaxTurns:    9,
		},
		RepoPath: os.TempDir(),
	}

	type outcome struct {
		r   agent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), input, logCh)
		close(logCh)
		ch <- outcome{r, err}
	}()
	for range logCh {
	}
	res := <-ch

	var mt *agent.ErrMaxTurns
	if e, ok := res.err.(*agent.ErrMaxTurns); ok {
		mt = e
	}
	if mt == nil {
		t.Fatalf("want *agent.ErrMaxTurns, got err=%v (%T)", res.err, res.err)
	}
	if mt.MaxTurns != 9 {
		t.Errorf("want MaxTurns=9, got %d", mt.MaxTurns)
	}
	if res.r.Status != "failed" {
		t.Errorf("want Status=failed, got %q", res.r.Status)
	}
	if res.r.SessionID != "qwen-max-turns-session" {
		t.Errorf("want session id preserved, got %q", res.r.SessionID)
	}
	if res.r.InputTokens != 30 || res.r.OutputTokens != 40 {
		t.Errorf("want usage preserved (30/40), got (%d/%d)", res.r.InputTokens, res.r.OutputTokens)
	}
}

// --- Subprocess lifecycle tests (generalized from claude_test.go's
// CLAUDE_TEST_HELPER re-exec harness — see TestMain in claude_test.go) ---

func qwenHelperInput(mode string) agent.RunInput {
	return agent.RunInput{
		RunID:       "qwen-test-run",
		Task:        agent.Task{ID: "task-1", Title: "test task"},
		AgentConfig: agent.AgentConfig{Env: map[string]string{"QWEN_TEST_HELPER": mode}, TimeoutSecs: 10},
		RepoPath:    os.TempDir(),
	}
}

func TestQwenRunner_Binary_DefaultsAndOverrides(t *testing.T) {
	var r QwenRunner
	if got := r.binary(); got != "qwen" {
		t.Errorf("binary() = %q, want default %q", got, "qwen")
	}
	r.BinaryPath = "/opt/qwen"
	if got := r.binary(); got != "/opt/qwen" {
		t.Errorf("binary() = %q, want override %q", got, "/opt/qwen")
	}
}

func TestQwenRunner_Run_Exit0Success(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), qwenHelperInput("exit0_success"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", result.Outcome)
	}
	if result.InputTokens != 5 || result.OutputTokens != 7 {
		t.Errorf("usage = %d/%d, want 5/7", result.InputTokens, result.OutputTokens)
	}
}

func TestQwenRunner_Run_Exit1NoOutputIsFailed(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), qwenHelperInput("exit1_no_output"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
}

// TestQwenRunner_Run_Exit1WithOutcome_OutcomeWins documents that, unlike
// claude's runner (which explicitly re-classifies a parsed success outcome
// as failed when the exit code is non-zero — see
// TestClaudeExitCode1_IsFailed), qwen.go has no such override: a non-empty
// parsed outcome wins over the exit code. This is a real, if surprising,
// behavioral difference between the two providers worth having pinned down
// by a test rather than left as tribal knowledge.
func TestQwenRunner_Run_Exit1WithOutcome_OutcomeWins(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), qwenHelperInput("exit1_with_outcome"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed (outcome wins over exit code for qwen)", result.Status)
	}
	if result.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", result.Outcome)
	}
}

// TestQwenRunner_Run_TimeoutKillsProcess verifies the context-cancel/timeout
// branch (shared by every CLI provider) actually kills a hung process and
// returns *agent.ErrTransient instead of hanging.
func TestQwenRunner_Run_TimeoutKillsProcess(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := qwenHelperInput("")
	input.AgentConfig.Env["HANG_TEST_HELPER"] = "30"
	input.AgentConfig.TimeoutSecs = 1

	start := time.Now()
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Fatalf("Run took %s, want the 1s timeout to kill the process quickly", elapsed)
	}
	var te *agent.ErrTransient
	if !errors.As(err, &te) {
		t.Fatalf("want *agent.ErrTransient, got %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
}
