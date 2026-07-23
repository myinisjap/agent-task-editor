package providers

import (
	"context"
	"database/sql"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// modelPrice holds USD-per-1M-token pricing for a single model.
type modelPrice struct {
	InputPer1M  float64
	OutputPer1M float64
}

// modelPricing is a small, manually maintained table of approximate USD
// prices per 1M tokens for common models used by the anthropic/llm
// providers. It is intentionally approximate — pricing changes over time
// and varies by tier/region — and is only used to produce an *estimated*
// cost figure for budgeting purposes, not an authoritative bill. The
// `claude` CLI provider does not use this table; it reports its own
// authoritative `total_cost_usd` from the CLI's `result` message instead.
//
// Update this table as new model IDs ship or prices change. Unknown models
// fall back to a prefix match (see estimateCostUSD) and finally to $0 if no
// match is found — we deliberately do not guess at unknown pricing.
var modelPricing = map[string]modelPrice{
	// Anthropic Claude models
	"claude-opus-4":     {InputPer1M: 15, OutputPer1M: 75},
	"claude-opus-4-1":   {InputPer1M: 15, OutputPer1M: 75},
	"claude-sonnet-4":   {InputPer1M: 3, OutputPer1M: 15},
	"claude-sonnet-4-5": {InputPer1M: 3, OutputPer1M: 15},
	"claude-sonnet-5":   {InputPer1M: 3, OutputPer1M: 15},
	"claude-haiku-4-5":  {InputPer1M: 1, OutputPer1M: 5},
	"claude-3-5-sonnet": {InputPer1M: 3, OutputPer1M: 15},
	"claude-3-5-haiku":  {InputPer1M: 0.8, OutputPer1M: 4},
	"claude-3-opus":     {InputPer1M: 15, OutputPer1M: 75},
	"claude-3-haiku":    {InputPer1M: 0.25, OutputPer1M: 1.25},

	// OpenAI-compatible models (for the `llm` provider)
	"gpt-4o":       {InputPer1M: 2.5, OutputPer1M: 10},
	"gpt-4o-mini":  {InputPer1M: 0.15, OutputPer1M: 0.6},
	"gpt-4.1":      {InputPer1M: 2, OutputPer1M: 8},
	"gpt-4.1-mini": {InputPer1M: 0.4, OutputPer1M: 1.6},
	"gpt-4.1-nano": {InputPer1M: 0.1, OutputPer1M: 0.4},
	"o3":           {InputPer1M: 2, OutputPer1M: 8},
	"o3-mini":      {InputPer1M: 1.1, OutputPer1M: 4.4},
	"o4-mini":      {InputPer1M: 1.1, OutputPer1M: 4.4},
}

// lookupModelPrice looks up model in the given price table (exact match
// first, then longest-prefix match against known model families for forward
// compatibility with new dated model-ID suffixes) and reports whether a
// match was found.
func lookupModelPrice(table map[string]modelPrice, model string) (modelPrice, bool) {
	if model == "" {
		return modelPrice{}, false
	}
	price, ok := table[model]
	if !ok {
		var bestLen int
		for prefix, p := range table {
			if strings.HasPrefix(model, prefix) && len(prefix) > bestLen {
				price = p
				ok = true
				bestLen = len(prefix)
			}
		}
	}
	return price, ok
}

// estimateCostUSD looks up model in the hardcoded pricing table above and
// returns the estimated USD cost for the given token counts. Returns 0 if
// the model is unknown — we do not want to fabricate a cost figure for a
// model we have no pricing data for. This is the fallback resolver
// (defaultPriceResolver{}) used when no DB-backed pricing row matches; see
// PriceResolver for the DB-aware path wired up in cmd/server/main.go.
func estimateCostUSD(model string, inputTokens, outputTokens int64) float64 {
	price, ok := lookupModelPrice(modelPricing, model)
	if !ok {
		return 0
	}
	return (float64(inputTokens)/1_000_000)*price.InputPer1M + (float64(outputTokens)/1_000_000)*price.OutputPer1M
}

// PriceResolver resolves a model ID to USD-per-1M-token pricing, reporting
// whether the model was recognized at all (known=false lets callers flag a
// run's cost as "estimated as $0 because pricing is unknown" instead of
// silently treating it the same as a genuinely free run).
type PriceResolver interface {
	Price(ctx context.Context, model string) (inputPer1M, outputPer1M float64, known bool)
}

// defaultPriceResolver resolves purely against the hardcoded modelPricing
// map above. Used when a runner has no PriceResolver configured (e.g. in
// tests), and as the final fallback layer for the DB-backed resolver
// constructed in cmd/server/main.go.
type defaultPriceResolver struct{}

func (defaultPriceResolver) Price(_ context.Context, model string) (float64, float64, bool) {
	price, ok := lookupModelPrice(modelPricing, model)
	return price.InputPer1M, price.OutputPer1M, ok
}

// estimateCostUSDWithResolver computes cost using resolver (falling back to
// the hardcoded map if resolver is nil), returning both the estimated USD
// cost and whether the model's price was actually known. known=false with
// tokens > 0 means the cost figure is a placeholder $0, not a genuinely free
// run — callers should flag this rather than display it as an authoritative
// zero.
func estimateCostUSDWithResolver(ctx context.Context, resolver PriceResolver, model string, inputTokens, outputTokens int64) (cost float64, known bool) {
	if resolver == nil {
		resolver = defaultPriceResolver{}
	}
	inPer1M, outPer1M, known := resolver.Price(ctx, model)
	if !known {
		return 0, false
	}
	return (float64(inputTokens)/1_000_000)*inPer1M + (float64(outputTokens)/1_000_000)*outPer1M, true
}

// DBPriceResolver resolves pricing from the user-editable model_pricing
// table (see 042_model_pricing / GET/PUT /api/v1/settings/pricing) — exact
// match first, then the same longest-prefix match the hardcoded fallback
// table uses (see lookupModelPrice) — falling back to the hardcoded
// modelPricing map entirely when no DB row matches either way. It reads the
// DB fresh on every call rather than caching at construction time, so an
// edit made through the settings UI takes effect on the very next run
// without a process restart. Constructed once in cmd/server/main.go's
// providerFactory and shared across runs.
type DBPriceResolver struct {
	Q *gen.Queries
}

func (r DBPriceResolver) Price(ctx context.Context, model string) (float64, float64, bool) {
	if model == "" {
		return 0, 0, false
	}
	if r.Q != nil {
		// Exact match first (single-row lookup, cheapest common case).
		if row, err := r.Q.GetModelPricing(ctx, model); err == nil {
			return row.InputPer1m, row.OutputPer1m, true
		} else if err != sql.ErrNoRows {
			// DB error other than "not found" — fall through to the hardcoded
			// map rather than silently treating this as an unpriced model.
			_ = err
		} else if rows, lerr := r.Q.ListModelPricing(ctx); lerr == nil {
			// No exact match — try the same longest-prefix match the hardcoded
			// fallback table uses, so a user-added row (e.g. "claude-sonnet-4-5")
			// also prices dated/suffixed model IDs (e.g.
			// "claude-sonnet-4-5-20260101") the same way the fallback does,
			// instead of only ever matching exact model strings.
			table := make(map[string]modelPrice, len(rows))
			for _, row := range rows {
				table[row.Model] = modelPrice{InputPer1M: row.InputPer1m, OutputPer1M: row.OutputPer1m}
			}
			if price, ok := lookupModelPrice(table, model); ok {
				return price.InputPer1M, price.OutputPer1M, true
			}
		}
	}
	price, ok := lookupModelPrice(modelPricing, model)
	return price.InputPer1M, price.OutputPer1M, ok
}
