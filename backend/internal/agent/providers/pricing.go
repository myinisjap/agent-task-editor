package providers

import "strings"

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

// estimateCostUSD looks up model in the pricing table (exact match first,
// then longest-prefix match against known model families for forward
// compatibility with new dated model-ID suffixes) and returns the estimated
// USD cost for the given token counts. Returns 0 if the model is unknown —
// we do not want to fabricate a cost figure for a model we have no pricing
// data for.
func estimateCostUSD(model string, inputTokens, outputTokens int64) float64 {
	if model == "" {
		return 0
	}
	price, ok := modelPricing[model]
	if !ok {
		var bestLen int
		for prefix, p := range modelPricing {
			if strings.HasPrefix(model, prefix) && len(prefix) > bestLen {
				price = p
				ok = true
				bestLen = len(prefix)
			}
		}
	}
	if !ok {
		return 0
	}
	return (float64(inputTokens)/1_000_000)*price.InputPer1M + (float64(outputTokens)/1_000_000)*price.OutputPer1M
}
