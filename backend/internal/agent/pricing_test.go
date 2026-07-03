package agent

import "testing"

// TestEstimateCostUSD_ExactMatch verifies exact model-ID lookups compute the
// expected cost from the pricing table.
func TestEstimateCostUSD_ExactMatch(t *testing.T) {
	got := estimateCostUSD("gpt-4o", 1_000_000, 1_000_000)
	want := 2.5 + 10.0
	if got != want {
		t.Errorf("want %v, got %v", want, got)
	}
}

// TestEstimateCostUSD_PrefixFallback verifies that an unknown but
// prefix-compatible model ID (e.g. a new dated suffix) falls back to the
// longest matching known prefix's pricing.
func TestEstimateCostUSD_PrefixFallback(t *testing.T) {
	got := estimateCostUSD("claude-sonnet-4-5-20260101", 1_000_000, 1_000_000)
	want := 3.0 + 15.0
	if got != want {
		t.Errorf("want %v, got %v", want, got)
	}
}

// TestEstimateCostUSD_LongestPrefixWins verifies that when multiple table
// entries are prefixes of the model ID, the longest (most specific) one is
// used rather than an arbitrary match.
func TestEstimateCostUSD_LongestPrefixWins(t *testing.T) {
	// "claude-sonnet-4" and "claude-sonnet-4-5" are both prefixes of
	// "claude-sonnet-4-5-foo"; the longer/more specific one should win.
	got := estimateCostUSD("claude-sonnet-4-5-foo", 1_000_000, 0)
	want := 3.0 // claude-sonnet-4-5 InputPer1M
	if got != want {
		t.Errorf("want %v, got %v", want, got)
	}
}

// TestEstimateCostUSD_UnknownModel verifies unknown models return 0 rather
// than a fabricated estimate.
func TestEstimateCostUSD_UnknownModel(t *testing.T) {
	got := estimateCostUSD("some-totally-unknown-model", 1_000_000, 1_000_000)
	if got != 0 {
		t.Errorf("want 0 for unknown model, got %v", got)
	}
}

// TestEstimateCostUSD_EmptyModel verifies an empty model string returns 0.
func TestEstimateCostUSD_EmptyModel(t *testing.T) {
	got := estimateCostUSD("", 1000, 1000)
	if got != 0 {
		t.Errorf("want 0 for empty model, got %v", got)
	}
}

// TestEstimateCostUSD_ZeroTokens verifies zero token counts yield zero cost
// even for a known model.
func TestEstimateCostUSD_ZeroTokens(t *testing.T) {
	got := estimateCostUSD("gpt-4o", 0, 0)
	if got != 0 {
		t.Errorf("want 0 for zero tokens, got %v", got)
	}
}
