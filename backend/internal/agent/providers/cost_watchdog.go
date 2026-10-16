package providers

import "context"

// costWatchdog projects a run's total cost (prior-runs spend plus this run's
// cumulative token usage, priced via resolver) as incremental usage arrives,
// and reports when that projection crosses the task's warning threshold or
// hard budget. It is deliberately a pure, side-effect-free accumulator — the
// caller (claude.go/qwen.go's stdout scan loop) owns cancelling the
// subprocess and constructing agent.ErrCostBudgetExceeded.
//
// Cost here is an *estimate*: it is derived from incremental token counts via
// the pricing table (see pricing.go), not the provider's own authoritative
// billed cost, which (for claude/qwen) is only known once the run's terminal
// "result" event arrives — too late for a mid-run kill switch to use. Under a
// subscription plan where the real marginal cost is $0, this estimate can
// still be nonzero and can still trigger a kill; operators relying on budgets
// under a subscription should size MaxCostUSD generously or leave it unset.
// See docs/agents.md's "Cost Budgets" section.
type costWatchdog struct {
	budget    float64
	spent     float64
	warnRatio float64
	resolver  PriceResolver
	model     string

	warned bool
}

// newCostWatchdog constructs a watchdog for one run. budget<=0 disables the
// watchdog entirely (observe always reports no warn/exceeded). warnRatio<=0
// or >1 disables the early warning but leaves the hard kill switch active.
func newCostWatchdog(budget, spent, warnRatio float64, resolver PriceResolver, model string) *costWatchdog {
	return &costWatchdog{budget: budget, spent: spent, warnRatio: warnRatio, resolver: resolver, model: model}
}

// active reports whether this watchdog can do anything useful — i.e. a
// budget is configured. Callers should skip the pricing lookup entirely
// (and thus never risk misclassifying an unpriced model as $0 exceeded) when
// this is false.
func (w *costWatchdog) active() bool {
	return w != nil && w.budget > 0
}

// setModelIfEmpty fills in model the first time it's learned, if the
// watchdog wasn't constructed with one. Used when AgentConfig.Model is empty
// (the operator left it unset, so the CLI picked its own default) — the
// stream-json "system"/"init" event reports which model the CLI actually
// resolved to (see claude.go/qwen.go), and without this fallback the
// watchdog would stay permanently inert (Price never finds a match) for the
// entire run. A no-op once model is already set, so an explicitly configured
// model is never silently overridden.
func (w *costWatchdog) setModelIfEmpty(model string) {
	if w != nil && w.model == "" && model != "" {
		w.model = model
	}
}

// observe takes the run's *cumulative* token usage so far (not a per-message
// delta — see claude.go/qwen.go, which track a running total across
// assistant-message events, deduping by message.id) and returns the current
// projected total cost, whether this call newly crosses the warning
// threshold (fires at most once per watchdog instance), and whether it
// crosses the hard budget.
//
// cacheReadTokens/cacheWriteTokens are claude/qwen's cache_read_input_tokens/
// cache_creation_input_tokens counters — additional to inputTokens, not a
// subset of it (see runUsage) — priced at their own resolver rates via
// estimateClaudeCostUSD (cache reads well below, cache writes somewhat
// above, the full input rate) rather than at the full input rate, which used
// to over-project cost by several times on a typical cache-heavy agent run.
// Callers with no cache counters to report (or a provider that doesn't
// distinguish them) pass 0 for both, which is equivalent to the old
// input/output-only projection.
//
// known=false (model not in the pricing table) always reports
// (0, false, false) — an unpriced model can't be projected, so the watchdog
// is a silent no-op rather than risking a false-positive kill from a bogus
// $0/$∞ estimate.
func (w *costWatchdog) observe(ctx context.Context, inputTokens, cacheReadTokens, cacheWriteTokens, outputTokens int64) (projected float64, warn bool, exceeded bool) {
	if !w.active() {
		return 0, false, false
	}
	incremental, known := estimateClaudeCostUSD(ctx, w.resolver, w.model, inputTokens, cacheReadTokens, cacheWriteTokens, outputTokens)
	if !known {
		return 0, false, false
	}
	projected = w.spent + incremental
	if w.warnRatio > 0 && w.warnRatio <= 1 && !w.warned && projected >= w.warnRatio*w.budget {
		w.warned = true
		warn = true
	}
	exceeded = projected >= w.budget
	return projected, warn, exceeded
}
