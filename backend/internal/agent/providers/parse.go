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
	// CachedInputTokens is the part of InputTokens served from the prompt
	// cache, for providers that report it as a *subset* of InputTokens
	// (codex_cli only). Priced at the model's cached-input rate when one is
	// known (see estimateCachedCostUSD). claude/qwen use the separate
	// CacheReadInputTokens/CacheCreationInputTokens fields below instead —
	// their CLI reports cache tokens as additional counters alongside (not
	// a subset of) input_tokens, a different shape from codex's.
	CachedInputTokens int64
	// CacheReadInputTokens is the claude/qwen stream-json
	// cache_read_input_tokens counter: prompt-cache hits, priced well below
	// the full input rate (see pricing.go's CacheReadInputPer1M). Unlike
	// codex's CachedInputTokens, this is reported *in addition to*
	// InputTokens, not as a subset of it.
	CacheReadInputTokens int64
	// CacheCreationInputTokens is the claude/qwen stream-json
	// cache_creation_input_tokens counter: tokens written to the prompt
	// cache for the first time this turn, priced *above* the full input
	// rate (see pricing.go's CacheWriteInputPer1M). Also additional to
	// InputTokens, not a subset.
	CacheCreationInputTokens int64
	CostUSD                  float64
	// Turns is the run's actual turn count as reported by the provider
	// (claude/qwen stream-json's result-event num_turns). 0 when the message
	// carries no turn count — never estimated. See agent.Result.TurnsUsed.
	Turns int64
}

// applyUsage copies token/cost usage from u onto res, if u is non-nil.
// res.InputTokens includes u's cache read/creation tokens (claude/qwen
// report those as separate counters, not a subset of InputTokens — see
// runUsage) so the persisted/displayed token count reflects everything the
// run actually consumed; res.CostUSD is left as u.CostUSD (for claude/qwen
// this is always the CLI's own authoritative total_cost_usd, already priced
// correctly per-token-type by Anthropic — this function never re-estimates
// it).
func applyUsage(res *agent.Result, u *runUsage) {
	if u == nil {
		return
	}
	res.InputTokens = u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	res.OutputTokens = u.OutputTokens
	res.CostUSD = u.CostUSD
	res.TurnsUsed = u.Turns
}

// applyUsageWithCost copies token usage from u onto res (like applyUsage)
// and additionally prices it: if u already carries an authoritative
// provider-reported cost (u.CostUSD > 0 — e.g. opencode's step_finish
// "cost" field), that value is kept as-is and res.CostUnknown is left
// false. Otherwise the tokens are priced via resolver/model using
// estimateCachedCostUSD (the same DB-backed pricing table used for cost
// estimation, with u.CachedInputTokens at the cached-input rate), and
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
	cost, known := estimateCachedCostUSD(ctx, resolver, model, u.InputTokens, u.CachedInputTokens, u.OutputTokens)
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

// isMaxTurnsLine reports whether the line signals that the agent hit its
// configured turn cap. Thin wrapper over agent.ClassifyLine. Used by the
// qwen_code provider, whose CLI (verified against @qwen-code/qwen-code
// 0.25.2) prints this to stderr and exits non-zero instead of emitting a
// stream-json result with subtype:"error_max_turns" the way claude does —
// see errclass.go and qwen.go.
func isMaxTurnsLine(line string) bool {
	return agent.ClassifyLine(line) == agent.ClassMaxTurns
}
