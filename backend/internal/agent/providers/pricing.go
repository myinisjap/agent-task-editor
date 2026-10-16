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
	// CachedInputPer1M is the price of cached (prompt-cache hit) input
	// tokens; nil bills them at InputPer1M. Used by any provider that
	// reports a cached-token count: codex_cli's cached_input_tokens (a
	// subset of input_tokens) and claude/qwen's cache_read_input_tokens (a
	// separate counter, additional to input_tokens — see runUsage). See
	// CachedPriceResolver.
	CachedInputPer1M *float64
	// CacheWriteInputPer1M is the price of cache *write* tokens — claude/
	// qwen's cache_creation_input_tokens, billed at a premium over the base
	// input rate for writing a prompt prefix into the cache for the first
	// time. nil bills them at InputPer1M (the pre-this-feature behavior).
	// Only meaningful for claude/qwen; codex_cli reports no cache-write
	// counter. See CacheWritePriceResolver.
	CacheWriteInputPer1M *float64
}

func usd(v float64) *float64 { return &v }

// modelPricing is a small, manually maintained table of approximate USD
// prices per 1M tokens for common models used by the
// providers that estimate cost from tokens. It is intentionally approximate — pricing changes over time
// and varies by tier/region — and is only used to produce an *estimated*
// cost figure for budgeting purposes, not an authoritative bill. The
// `claude` CLI's own run cost (Result.CostUSD) is always the CLI's
// authoritative `total_cost_usd` from its `result` message, never estimated
// from this table — this table is only consulted by claude's *mid-run cost
// watchdog* (cost_watchdog.go), which has no access to that figure until
// the run is already over.
//
// Anthropic Claude rows below (input/output/cache-write/cache-read) were
// cross-checked against Anthropic's published pricing page as of this
// change; cache reads are consistently ~10% of the base input price and
// cache writes ~125% of it. Update this table as new model IDs ship or
// prices change. Unknown models fall back to a prefix match (see
// estimateCostUSD) and finally to $0 if no match is found — we deliberately
// do not guess at unknown pricing or unverified model IDs.
var modelPricing = map[string]modelPrice{
	// Anthropic Claude models, cross-checked against Anthropic's published
	// pricing page (anthropic.com/pricing) as of this change. Retired
	// dateless aliases (claude-opus-4, claude-sonnet-4, claude-3-5-sonnet,
	// claude-3-5-haiku, claude-3-opus, claude-3-haiku) keep their
	// last-published prices for historical runs; cache rates there follow
	// the standard 0.1x read / 1.25x write multiplier Anthropic has used
	// consistently across every generation. We deliberately do NOT invent
	// prices for model names we can't verify — only add a row here once its
	// price is confirmed from Anthropic's own pricing page/announcement.
	"claude-opus-4":     {InputPer1M: 15, OutputPer1M: 75, CachedInputPer1M: usd(1.50), CacheWriteInputPer1M: usd(18.75)},
	"claude-opus-4-1":   {InputPer1M: 15, OutputPer1M: 75, CachedInputPer1M: usd(1.50), CacheWriteInputPer1M: usd(18.75)},
	"claude-opus-4-5":   {InputPer1M: 5, OutputPer1M: 25, CachedInputPer1M: usd(0.50), CacheWriteInputPer1M: usd(6.25)},
	"claude-sonnet-4":   {InputPer1M: 3, OutputPer1M: 15, CachedInputPer1M: usd(0.30), CacheWriteInputPer1M: usd(3.75)},
	"claude-sonnet-4-5": {InputPer1M: 3, OutputPer1M: 15, CachedInputPer1M: usd(0.30), CacheWriteInputPer1M: usd(3.75)},
	"claude-sonnet-5":   {InputPer1M: 3, OutputPer1M: 15, CachedInputPer1M: usd(0.30), CacheWriteInputPer1M: usd(3.75)},
	"claude-haiku-4-5":  {InputPer1M: 1, OutputPer1M: 5, CachedInputPer1M: usd(0.10), CacheWriteInputPer1M: usd(1.25)},
	"claude-3-5-sonnet": {InputPer1M: 3, OutputPer1M: 15, CachedInputPer1M: usd(0.30), CacheWriteInputPer1M: usd(3.75)},
	"claude-3-5-haiku":  {InputPer1M: 0.8, OutputPer1M: 4, CachedInputPer1M: usd(0.08), CacheWriteInputPer1M: usd(1)},
	"claude-3-opus":     {InputPer1M: 15, OutputPer1M: 75, CachedInputPer1M: usd(1.50), CacheWriteInputPer1M: usd(18.75)},
	"claude-3-haiku":    {InputPer1M: 0.25, OutputPer1M: 1.25, CachedInputPer1M: usd(0.03), CacheWriteInputPer1M: usd(0.3125)},

	// OpenAI models (codex_cli), with their published cached-input rates.
	"gpt-4o":       {InputPer1M: 2.5, OutputPer1M: 10, CachedInputPer1M: usd(1.25)},
	"gpt-4o-mini":  {InputPer1M: 0.15, OutputPer1M: 0.6, CachedInputPer1M: usd(0.075)},
	"gpt-4.1":      {InputPer1M: 2, OutputPer1M: 8, CachedInputPer1M: usd(0.5)},
	"gpt-4.1-mini": {InputPer1M: 0.4, OutputPer1M: 1.6, CachedInputPer1M: usd(0.1)},
	"gpt-4.1-nano": {InputPer1M: 0.1, OutputPer1M: 0.4, CachedInputPer1M: usd(0.025)},
	"o3":           {InputPer1M: 2, OutputPer1M: 8, CachedInputPer1M: usd(0.5)},
	"o3-mini":      {InputPer1M: 1.1, OutputPer1M: 4.4, CachedInputPer1M: usd(0.55)},
	"o4-mini":      {InputPer1M: 1.1, OutputPer1M: 4.4, CachedInputPer1M: usd(0.275)},
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

// CachedPriceResolver is optionally implemented by a PriceResolver that also
// knows a model's cached-input rate. ok=false means no cached rate is known
// (the model is unknown, or its row leaves the rate unset), and callers bill
// cached tokens at the regular input rate.
type CachedPriceResolver interface {
	CachedInputPrice(ctx context.Context, model string) (cachedPer1M float64, ok bool)
}

func (defaultPriceResolver) CachedInputPrice(_ context.Context, model string) (float64, bool) {
	price, ok := lookupModelPrice(modelPricing, model)
	if !ok || price.CachedInputPer1M == nil {
		return 0, false
	}
	return *price.CachedInputPer1M, true
}

// estimateCachedCostUSD is estimateCostUSDWithResolver for a usage report that
// splits out cached input tokens (cachedTokens is a subset of inputTokens,
// as codex reports it): the cached part is billed at the resolver's cached
// rate when it has one, else at the input rate like any other input token.
func estimateCachedCostUSD(ctx context.Context, resolver PriceResolver, model string, inputTokens, cachedTokens, outputTokens int64) (cost float64, known bool) {
	if resolver == nil {
		resolver = defaultPriceResolver{}
	}
	inPer1M, outPer1M, known := resolver.Price(ctx, model)
	if !known {
		return 0, false
	}
	cachedTokens = min(max(cachedTokens, 0), inputTokens)
	cachedPer1M := inPer1M
	if cr, ok := resolver.(CachedPriceResolver); ok {
		if v, ok := cr.CachedInputPrice(ctx, model); ok {
			cachedPer1M = v
		}
	}
	return (float64(inputTokens-cachedTokens)/1_000_000)*inPer1M +
		(float64(cachedTokens)/1_000_000)*cachedPer1M +
		(float64(outputTokens)/1_000_000)*outPer1M, true
}

// CacheWritePriceResolver is optionally implemented by a PriceResolver that
// also knows a model's cache-write rate (claude/qwen's
// cache_creation_input_tokens). ok=false means no write rate is known (the
// model is unknown, or its row leaves the rate unset), and callers bill
// cache-write tokens at the regular input rate — the same conservative
// fallback estimateCachedCostUSD uses for cache reads.
type CacheWritePriceResolver interface {
	CacheWriteInputPrice(ctx context.Context, model string) (cacheWritePer1M float64, ok bool)
}

func (defaultPriceResolver) CacheWriteInputPrice(_ context.Context, model string) (float64, bool) {
	price, ok := lookupModelPrice(modelPricing, model)
	if !ok || price.CacheWriteInputPer1M == nil {
		return 0, false
	}
	return *price.CacheWriteInputPer1M, true
}

// estimateClaudeCostUSD prices a claude/qwen usage report, which — unlike
// codex's single cachedTokens subset — splits cache tokens into two
// counters *additional to* inputTokens: cacheReadTokens (prompt-cache hits,
// priced well below the input rate) and cacheWriteTokens (first-time cache
// writes, priced above it). Each bucket is billed at its own resolver rate
// when known, falling back to the plain input rate for whichever of
// read/write has none (so an operator who only fills in one of the two
// optional DB columns still gets a better-than-nothing estimate for the
// other, rather than the watchdog going fully inert). known=false (model
// not recognized at all) returns (0, false) exactly like
// estimateCostUSDWithResolver, so callers can treat an unpriced model as a
// no-op rather than risking a bogus $0/$∞ projection.
func estimateClaudeCostUSD(ctx context.Context, resolver PriceResolver, model string, inputTokens, cacheReadTokens, cacheWriteTokens, outputTokens int64) (cost float64, known bool) {
	if resolver == nil {
		resolver = defaultPriceResolver{}
	}
	inPer1M, outPer1M, known := resolver.Price(ctx, model)
	if !known {
		return 0, false
	}
	readPer1M := inPer1M
	if cr, ok := resolver.(CachedPriceResolver); ok {
		if v, ok := cr.CachedInputPrice(ctx, model); ok {
			readPer1M = v
		}
	}
	writePer1M := inPer1M
	if cw, ok := resolver.(CacheWritePriceResolver); ok {
		if v, ok := cw.CacheWriteInputPrice(ctx, model); ok {
			writePer1M = v
		}
	}
	return (float64(inputTokens)/1_000_000)*inPer1M +
		(float64(max(cacheReadTokens, 0))/1_000_000)*readPer1M +
		(float64(max(cacheWriteTokens, 0))/1_000_000)*writePer1M +
		(float64(outputTokens)/1_000_000)*outPer1M, true
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
	price, ok := r.lookup(ctx, model)
	return price.InputPer1M, price.OutputPer1M, ok
}

// CachedInputPrice resolves the cached-input rate from the same row Price
// matched; a row with no cached rate reports ok=false (bill at input rate).
func (r DBPriceResolver) CachedInputPrice(ctx context.Context, model string) (float64, bool) {
	price, ok := r.lookup(ctx, model)
	if !ok || price.CachedInputPer1M == nil {
		return 0, false
	}
	return *price.CachedInputPer1M, true
}

// CacheWriteInputPrice resolves the cache-write rate from the same row Price
// matched; a row with no cache-write rate reports ok=false (bill at input
// rate).
func (r DBPriceResolver) CacheWriteInputPrice(ctx context.Context, model string) (float64, bool) {
	price, ok := r.lookup(ctx, model)
	if !ok || price.CacheWriteInputPer1M == nil {
		return 0, false
	}
	return *price.CacheWriteInputPer1M, true
}

func (r DBPriceResolver) lookup(ctx context.Context, model string) (modelPrice, bool) {
	if model == "" {
		return modelPrice{}, false
	}
	if r.Q != nil {
		// Exact match first (single-row lookup, cheapest common case).
		if row, err := r.Q.GetModelPricing(ctx, model); err == nil {
			return modelPrice{InputPer1M: row.InputPer1m, OutputPer1M: row.OutputPer1m, CachedInputPer1M: row.CachedInputPer1m, CacheWriteInputPer1M: row.CacheWriteInputPer1m}, true
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
				table[row.Model] = modelPrice{InputPer1M: row.InputPer1m, OutputPer1M: row.OutputPer1m, CachedInputPer1M: row.CachedInputPer1m, CacheWriteInputPer1M: row.CacheWriteInputPer1m}
			}
			if price, ok := lookupModelPrice(table, model); ok {
				return price, true
			}
		}
	}
	return lookupModelPrice(modelPricing, model)
}
