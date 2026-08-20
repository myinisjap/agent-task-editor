package providers

import (
	"context"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// runUsage carries token usage and cost parsed from a single provider
// message (e.g. the claude/qwen CLI stream-json "result" envelope).
type runUsage struct {
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
	// Turns is the run's actual turn count as reported by the provider
	// (claude/qwen stream-json's result-event num_turns). 0 when the message
	// carries no turn count — never estimated. See agent.Result.TurnsUsed.
	Turns int64
}

// applyUsage copies token/cost usage from u onto res, if u is non-nil.
func applyUsage(res *agent.Result, u *runUsage) {
	if u == nil {
		return
	}
	res.InputTokens = u.InputTokens
	res.OutputTokens = u.OutputTokens
	res.CostUSD = u.CostUSD
	res.TurnsUsed = u.Turns
}

// applyUsageWithCost copies token usage from u onto res (like applyUsage)
// and additionally prices it: if u already carries an authoritative
// provider-reported cost (u.CostUSD > 0 — e.g. opencode's step_finish
// "cost" field), that value is kept as-is and res.CostUnknown is left
// false. Otherwise the tokens are priced via resolver/model using
// estimateCostUSDWithResolver (the same DB-backed pricing table the
// anthropic/llm path uses — see runAccumulators.attach in tools.go), and
// res.CostUnknown is set when tokens were actually consumed but no price
// was found, so the UI can render "cost unknown" instead of a misleading
// $0 (see providers.PriceResolver).
//
// Callers should pass the run's parent context (not a possibly-cancelled/
// timed-out per-run context) so the pricing DB lookup isn't starved on a
// run that already hit its deadline.
func applyUsageWithCost(ctx context.Context, res *agent.Result, u *runUsage, resolver PriceResolver, model string) {
	if u == nil {
		return
	}
	res.InputTokens = u.InputTokens
	res.OutputTokens = u.OutputTokens
	res.TurnsUsed = u.Turns
	if u.CostUSD > 0 {
		res.CostUSD = u.CostUSD
		return
	}
	cost, known := estimateCostUSDWithResolver(ctx, resolver, model, u.InputTokens, u.OutputTokens)
	res.CostUSD = cost
	res.CostUnknown = (u.InputTokens > 0 || u.OutputTokens > 0) && !known
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
