package providers

import "strings"

// isResumeErrorLine matches a CLI's session-not-found error for a bad
// --resume/--session id (best-effort text match, same spirit as
// errclass.go). Shared across claude, qwen_code and opencode: all three
// report a missing resume target with wording that falls into one of these
// two shapes ("no conversation found" / "session ... not found"), so one
// generic matcher covers them instead of a per-provider pattern. codex_cli
// has its own distinct wording ("no rollout found for thread id") matched
// separately in codex.go (codexMissingRolloutMarker).
func isResumeErrorLine(line string) bool {
	l := strings.ToLower(line)
	return strings.Contains(l, "no conversation found") ||
		(strings.Contains(l, "session") && strings.Contains(l, "not found"))
}
