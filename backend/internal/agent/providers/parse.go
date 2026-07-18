package providers

import (
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// runUsage carries token usage and cost parsed from a single provider
// message (e.g. the claude/qwen CLI stream-json "result" envelope).
type runUsage struct {
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
}

// applyUsage copies token/cost usage from u onto res, if u is non-nil.
func applyUsage(res *agent.Result, u *runUsage) {
	if u == nil {
		return
	}
	res.InputTokens = u.InputTokens
	res.OutputTokens = u.OutputTokens
	res.CostUSD = u.CostUSD
}

// extractOutcome looks for "OUTCOME: success|failure" anywhere in the text.
func extractOutcome(text string) string {
	const marker = "OUTCOME:"
	idx := strings.Index(text, marker)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(text[idx+len(marker):])
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	v := strings.ToLower(fields[0])
	if v == "success" || v == "failure" {
		return v
	}
	return ""
}

// is429Line reports whether the line signals an API rate-limit rejection.
// Thin wrapper over agent.ClassifyLine kept for the CLI providers' stdout/
// stderr scan loops.
func is429Line(line string) bool {
	return agent.ClassifyLine(line) == agent.ClassRateLimit
}

// isTransientLine reports whether the line signals a transient infrastructure
// problem (network blip, upstream 5xx, connection reset, timeout) rather than
// a genuine task/agent failure. Thin wrapper over agent.ClassifyLine.
func isTransientLine(line string) bool {
	return agent.ClassifyLine(line) == agent.ClassTransient
}
