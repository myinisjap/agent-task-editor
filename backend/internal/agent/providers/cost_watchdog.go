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

// observe takes the run's *cumulative* token usage so far (not a per-message
// delta — see claude.go/qwen.go, which track a running total across
// assistant-message events) and returns the current projected total cost,
// whether this call newly crosses the warning threshold (fires at most once
// per watchdog instance), and whether it crosses the hard budget.
//
// known=false (model not in the pricing table) always reports
// (0, false, false) — an unpriced model can't be projected, so the watchdog
// is a silent no-op rather than risking a false-positive kill from a bogus
// $0/$∞ estimate.
func (w *costWatchdog) observe(ctx context.Context, inputTokens, outputTokens int64) (projected float64, warn bool, exceeded bool) {
	if !w.active() {
		return 0, false, false
	}
	incremental, known := estimateCostUSDWithResolver(ctx, w.resolver, w.model, inputTokens, outputTokens)
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
