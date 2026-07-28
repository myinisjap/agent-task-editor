package providers

import (
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
