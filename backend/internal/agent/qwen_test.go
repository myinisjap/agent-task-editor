package agent

import "testing"

// TestBuildQwenArgs_MaxTurnsDefault verifies that when AgentConfig.MaxTurns
// is unset (zero), the constructed args default --max-turns to 50 (matching
// the claude provider's fallback behavior).
func TestBuildQwenArgs_MaxTurnsDefault(t *testing.T) {
	args := buildQwenArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{},
	}, nil)
	if got := findFlagValue(args, "--max-turns"); got != "50" {
		t.Fatalf("expected default --max-turns 50, got %q (args=%v)", got, args)
	}
}

// TestBuildQwenArgs_MaxTurnsConfigured verifies that a custom
// AgentConfig.MaxTurns value is passed through to the --max-turns flag
// instead of the hardcoded default.
func TestBuildQwenArgs_MaxTurnsConfigured(t *testing.T) {
	args := buildQwenArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{MaxTurns: 7},
	}, nil)
	if got := findFlagValue(args, "--max-turns"); got != "7" {
		t.Fatalf("expected --max-turns 7, got %q (args=%v)", got, args)
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
	args := buildQwenArgs(RunInput{
		Task: Task{Title: "t"},
		AgentConfig: AgentConfig{
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
	args := buildQwenArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{},
	}, nil)
	for i, a := range args {
		if a == "--allowed-tools" {
			t.Fatalf("expected no --allowed-tools flags without mcpCfg/allowlist, found one at index %d in args=%v", i, args)
		}
	}
}
