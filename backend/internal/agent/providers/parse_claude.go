package providers

import "strings"

// isResumeErrorLine matches the claude CLI's session-not-found error for a bad
// --resume id (best-effort text match, same spirit as errclass.go).
func isResumeErrorLine(line string) bool {
	l := strings.ToLower(line)
	return strings.Contains(l, "no conversation found") ||
		(strings.Contains(l, "session") && strings.Contains(l, "not found"))
}
