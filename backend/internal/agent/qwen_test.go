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
