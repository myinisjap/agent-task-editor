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

// TestBuildQwenArgs_CommandAllowlist verifies that each CommandAllowlist
// pattern is appended as a Bash(pattern) entry to --allowed-tools.
func TestBuildQwenArgs_CommandAllowlist(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task: agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{
			CommandAllowlist: []string{"git *", "npm test"},
		},
	}, nil)
	if countFlagOccurrences(args, "--allowed-tools", "Bash(git *)") != 1 {
		t.Fatalf("expected one --allowed-tools Bash(git *) entry, got args=%v", args)
	}
	if countFlagOccurrences(args, "--allowed-tools", "Bash(npm test)") != 1 {
		t.Fatalf("expected one --allowed-tools Bash(npm test) entry, got args=%v", args)
	}
}

// TestBuildQwenArgs_NoCommandAllowlist_NoExtraFlags verifies that an empty
// CommandAllowlist adds no extra --allowed-tools entries (backward compatible).
func TestBuildQwenArgs_NoCommandAllowlist_NoExtraFlags(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
	}, nil)
	for i, a := range args {
		if a == "--allowed-tools" {
			t.Fatalf("expected no --allowed-tools flags without mcpCfg/allowlist, found one at index %d in args=%v", i, args)
		}
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
