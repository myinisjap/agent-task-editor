package providers

import (
	"context"
	"os"
	"testing"

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
