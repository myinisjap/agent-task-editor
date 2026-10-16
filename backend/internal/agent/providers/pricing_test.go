package providers

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

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

// fakePriceResolver lets tests control PriceResolver.Price without a DB.
type fakePriceResolver struct {
	inPer1M, outPer1M float64
	known             bool
}

func (f fakePriceResolver) Price(context.Context, string) (float64, float64, bool) {
	return f.inPer1M, f.outPer1M, f.known
}

// TestEstimateCostUSDWithResolver_NilFallsBackToMap verifies a nil resolver
// behaves exactly like the hardcoded-map-only estimateCostUSD.
func TestEstimateCostUSDWithResolver_NilFallsBackToMap(t *testing.T) {
	cost, known := estimateCostUSDWithResolver(context.Background(), nil, "gpt-4o", 1_000_000, 1_000_000)
	if !known {
		t.Fatal("expected gpt-4o to be known via the hardcoded map fallback")
	}
	want := estimateCostUSD("gpt-4o", 1_000_000, 1_000_000)
	if cost != want {
		t.Errorf("want %v, got %v", want, cost)
	}
}

// TestEstimateCostUSDWithResolver_UnknownFlagsCost verifies an unrecognized
// model reports known=false and cost 0, rather than silently returning 0
// indistinguishable from a genuinely free run.
func TestEstimateCostUSDWithResolver_UnknownFlagsCost(t *testing.T) {
	cost, known := estimateCostUSDWithResolver(context.Background(), fakePriceResolver{known: false}, "mystery-model", 1000, 1000)
	if known {
		t.Fatal("expected known=false for an unresolved model")
	}
	if cost != 0 {
		t.Errorf("want cost 0 when unknown, got %v", cost)
	}
}

// TestEstimateCostUSDWithResolver_UsesResolverPricing verifies the resolver's
// price (not the hardcoded map) is used when it reports known=true.
func TestEstimateCostUSDWithResolver_UsesResolverPricing(t *testing.T) {
	cost, known := estimateCostUSDWithResolver(context.Background(), fakePriceResolver{inPer1M: 10, outPer1M: 20, known: true}, "gpt-4o", 1_000_000, 1_000_000)
	if !known {
		t.Fatal("expected known=true")
	}
	want := 10.0 + 20.0
	if cost != want {
		t.Errorf("want %v, got %v", want, cost)
	}
}

// openTestDB opens a temp SQLite DB with migrations applied (including the
// seeded model_pricing table from 042_model_pricing).
func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp("", "pricing-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestDBPriceResolver_DBRowTakesPrecedence verifies an edited DB row
// overrides the hardcoded map for the same model.
func TestDBPriceResolver_DBRowTakesPrecedence(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	if _, err := q.UpsertModelPricing(context.Background(), gen.UpsertModelPricingParams{
		Model: "gpt-4o", InputPer1m: 100, OutputPer1m: 200,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	r := DBPriceResolver{Q: q}
	inPer1M, outPer1M, known := r.Price(context.Background(), "gpt-4o")
	if !known {
		t.Fatal("expected gpt-4o to be known")
	}
	if inPer1M != 100 || outPer1M != 200 {
		t.Errorf("expected DB-edited price to take precedence, got in=%v out=%v", inPer1M, outPer1M)
	}
}

// TestDBPriceResolver_FallsBackToMapWhenNotInDB verifies a model absent from
// the DB (e.g. deleted, or a brand new model never added) still resolves via
// the hardcoded map fallback.
func TestDBPriceResolver_FallsBackToMapWhenNotInDB(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	if err := q.DeleteAllModelPricing(context.Background()); err != nil {
		t.Fatalf("delete all: %v", err)
	}

	r := DBPriceResolver{Q: q}
	inPer1M, outPer1M, known := r.Price(context.Background(), "gpt-4o")
	if !known {
		t.Fatal("expected fallback to hardcoded map to still know gpt-4o")
	}
	if inPer1M != 2.5 || outPer1M != 10 {
		t.Errorf("expected hardcoded map pricing, got in=%v out=%v", inPer1M, outPer1M)
	}
}

// TestDBPriceResolver_PrefixMatchAgainstDBRow verifies a DB row is matched
// by longest-prefix (same as the hardcoded fallback table) rather than only
// ever exact-matching, so a user-added row for e.g. "claude-sonnet-4-5"
// also prices a dated/suffixed model ID that isn't itself a row.
func TestDBPriceResolver_PrefixMatchAgainstDBRow(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	// Replace the whole table with a single custom row so the hardcoded
	// map's own "claude-sonnet-4-5" entry can't mask a resolver bug.
	if err := q.DeleteAllModelPricing(context.Background()); err != nil {
		t.Fatalf("delete all: %v", err)
	}
	if _, err := q.UpsertModelPricing(context.Background(), gen.UpsertModelPricingParams{
		Model: "my-custom-model", InputPer1m: 42, OutputPer1m: 99,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	r := DBPriceResolver{Q: q}
	inPer1M, outPer1M, known := r.Price(context.Background(), "my-custom-model-20260101")
	if !known {
		t.Fatal("expected a dated suffix of a DB row's model ID to prefix-match")
	}
	if inPer1M != 42 || outPer1M != 99 {
		t.Errorf("expected the DB row's price via prefix match, got in=%v out=%v", inPer1M, outPer1M)
	}
}

// TestDBPriceResolver_UnknownEverywhere verifies a model in neither the DB
// nor the hardcoded map reports known=false.
func TestDBPriceResolver_UnknownEverywhere(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())

	r := DBPriceResolver{Q: q}
	_, _, known := r.Price(context.Background(), "some-totally-unknown-model")
	if known {
		t.Error("expected known=false for a model in neither the DB nor the hardcoded map")
	}
}

// TestDBPriceResolver_NilQueriesFallsBackToMap verifies a zero-value
// DBPriceResolver (Q == nil) still resolves via the hardcoded map — the
// static "default" path stays usable even if wiring the DB resolver was
// somehow skipped.
func TestDBPriceResolver_NilQueriesFallsBackToMap(t *testing.T) {
	var r DBPriceResolver
	inPer1M, outPer1M, known := r.Price(context.Background(), "gpt-4o")
	if !known {
		t.Fatal("expected gpt-4o to be known via the hardcoded map fallback")
	}
	if inPer1M != 2.5 || outPer1M != 10 {
		t.Errorf("expected hardcoded map pricing, got in=%v out=%v", inPer1M, outPer1M)
	}
}

// fakeCachedPriceResolver adds a cached-input rate to fakePriceResolver.
type fakeCachedPriceResolver struct {
	fakePriceResolver
	cachedPer1M float64
	cachedKnown bool
}

func (f fakeCachedPriceResolver) CachedInputPrice(context.Context, string) (float64, bool) {
	return f.cachedPer1M, f.cachedKnown
}

// TestEstimateCachedCostUSD verifies cached input tokens (a subset of input
// tokens) are billed at the cached rate when one is known, at the input rate
// otherwise, and that an out-of-range cached count is clamped.
func TestEstimateCachedCostUSD(t *testing.T) {
	ctx := context.Background()
	base := fakePriceResolver{inPer1M: 2, outPer1M: 8, known: true}
	cases := []struct {
		name            string
		resolver        PriceResolver
		in, cached, out int64
		want            float64
	}{
		{"cached rate", fakeCachedPriceResolver{base, 0.5, true}, 1_000_000, 800_000, 1_000_000, 0.2*2 + 0.8*0.5 + 8},
		{"no cached rate bills at input", fakeCachedPriceResolver{base, 0, false}, 1_000_000, 800_000, 0, 2},
		{"resolver without cached support", base, 1_000_000, 800_000, 0, 2},
		{"cached clamped to input", fakeCachedPriceResolver{base, 0.5, true}, 1_000_000, 5_000_000, 0, 0.5},
		{"negative cached ignored", fakeCachedPriceResolver{base, 0.5, true}, 1_000_000, -5, 0, 2},
	}
	for _, c := range cases {
		got, known := estimateCachedCostUSD(ctx, c.resolver, "m", c.in, c.cached, c.out)
		if !known || math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: got (%v, %v), want (%v, true)", c.name, got, known, c.want)
		}
	}
	if _, known := estimateCachedCostUSD(ctx, fakePriceResolver{}, "m", 1, 1, 1); known {
		t.Error("unknown model must report known=false")
	}
}

// fakeClaudeCacheResolver adds both a cache-read and a cache-write rate to
// fakePriceResolver, mirroring the two separate (additional-to-input, not a
// subset) counters claude/qwen report — see estimateClaudeCostUSD.
type fakeClaudeCacheResolver struct {
	fakePriceResolver
	readPer1M  float64
	readKnown  bool
	writePer1M float64
	writeKnown bool
}

func (f fakeClaudeCacheResolver) CachedInputPrice(context.Context, string) (float64, bool) {
	return f.readPer1M, f.readKnown
}

func (f fakeClaudeCacheResolver) CacheWriteInputPrice(context.Context, string) (float64, bool) {
	return f.writePer1M, f.writeKnown
}

// TestEstimateClaudeCostUSD verifies claude/qwen's cache read/write tokens
// (additional to, not a subset of, input tokens) are each billed at their
// own resolver rate when known, falling back to the plain input rate for
// whichever bucket has none — and that pricing cache reads at ~10% of the
// input rate (rather than the old behavior of lumping them into the full
// input rate) produces a materially lower estimate on a cache-heavy usage
// pattern typical of a long agent run.
func TestEstimateClaudeCostUSD(t *testing.T) {
	ctx := context.Background()
	base := fakePriceResolver{inPer1M: 3, outPer1M: 15, known: true}
	cases := []struct {
		name                           string
		resolver                       PriceResolver
		in, cacheRead, cacheWrite, out int64
		want                           float64
	}{
		{
			"both rates known",
			fakeClaudeCacheResolver{base, 0.3, true, 3.75, true},
			1_000, 1_000_000, 10_000, 500,
			(1_000.0/1e6)*3 + (1_000_000.0/1e6)*0.3 + (10_000.0/1e6)*3.75 + (500.0/1e6)*15,
		},
		{
			"no cache rates known falls back to input rate for both",
			base,
			1_000, 1_000_000, 10_000, 500,
			(1_000.0/1e6)*3 + (1_000_000.0/1e6)*3 + (10_000.0/1e6)*3 + (500.0/1e6)*15,
		},
		{
			"negative cache counts ignored (clamped to 0)",
			fakeClaudeCacheResolver{base, 0.3, true, 3.75, true},
			1_000, -5, -5, 0,
			(1_000.0 / 1e6) * 3,
		},
	}
	for _, c := range cases {
		got, known := estimateClaudeCostUSD(ctx, c.resolver, "m", c.in, c.cacheRead, c.cacheWrite, c.out)
		if !known || math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: got (%v, %v), want (%v, true)", c.name, got, known, c.want)
		}
	}
	if _, known := estimateClaudeCostUSD(ctx, fakePriceResolver{}, "m", 1, 1, 1, 1); known {
		t.Error("unknown model must report known=false")
	}

	// A cache-heavy run (mostly cache reads, as a long agent run typically
	// is) should project materially cheaper than pricing every token at the
	// full input rate — the bug this fixes.
	heavyResolver := fakeClaudeCacheResolver{fakePriceResolver{inPer1M: 3, outPer1M: 15, known: true}, 0.3, true, 3.75, true}
	cacheHeavy, _ := estimateClaudeCostUSD(ctx, heavyResolver, "m", 1_000, 10_000_000, 0, 1_000)
	allAtInputRate := (1_000.0/1e6)*3 + (10_000_000.0/1e6)*3 + (1_000.0/1e6)*15
	if cacheHeavy >= allAtInputRate {
		t.Errorf("cache-heavy estimate (%v) should be well below pricing all tokens at the full input rate (%v)", cacheHeavy, allAtInputRate)
	}
}

// TestDefaultPriceResolver_CachedInputPrice verifies the hardcoded table's
// cached rates are exposed, including through prefix matching. Claude rows
// now also carry a cached (cache-read) rate — see pricing.go's modelPricing
// map — so cache reads are no longer priced at the full input rate.
func TestDefaultPriceResolver_CachedInputPrice(t *testing.T) {
	v, ok := defaultPriceResolver{}.CachedInputPrice(context.Background(), "gpt-4.1-2025-04-14")
	if !ok || v != 0.5 {
		t.Errorf("gpt-4.1 cached = (%v, %v), want (0.5, true)", v, ok)
	}
	if v, ok := (defaultPriceResolver{}).CachedInputPrice(context.Background(), "claude-sonnet-4-5"); !ok || v != 0.30 {
		t.Errorf("claude-sonnet-4-5 cached = (%v, %v), want (0.30, true)", v, ok)
	}
}

// TestDBPriceResolver_CachedInputPrice verifies a DB row's cached rate is used
// (exact and prefix match), and a row without one reports ok=false.
func TestDBPriceResolver_CachedInputPrice(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()
	cached := 0.3
	for _, p := range []gen.UpsertModelPricingParams{
		{Model: "gpt-x", InputPer1m: 3, OutputPer1m: 9, CachedInputPer1m: &cached},
		{Model: "gpt-y", InputPer1m: 3, OutputPer1m: 9},
	} {
		if _, err := q.UpsertModelPricing(ctx, p); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	r := DBPriceResolver{Q: q}
	if v, ok := r.CachedInputPrice(ctx, "gpt-x"); !ok || v != 0.3 {
		t.Errorf("exact: got (%v, %v)", v, ok)
	}
	if v, ok := r.CachedInputPrice(ctx, "gpt-x-2026"); !ok || v != 0.3 {
		t.Errorf("prefix: got (%v, %v)", v, ok)
	}
	if _, ok := r.CachedInputPrice(ctx, "gpt-y"); ok {
		t.Error("row without cached rate should report ok=false")
	}
}

// TestModelPricingMigration_SeedsCachedRates verifies migration 065 filled the
// cached rate for the seeded OpenAI rows, and migration 066 backfilled the
// Claude rows' cache-read and cache-write rates.
func TestModelPricingMigration_SeedsCachedRates(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	row, err := q.GetModelPricing(context.Background(), "gpt-4o")
	if err != nil {
		t.Fatalf("get gpt-4o: %v", err)
	}
	if row.CachedInputPer1m == nil || *row.CachedInputPer1m != 1.25 {
		t.Errorf("gpt-4o cached = %v, want 1.25", row.CachedInputPer1m)
	}
	row, err = q.GetModelPricing(context.Background(), "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("get claude: %v", err)
	}
	if row.CachedInputPer1m == nil || *row.CachedInputPer1m != 0.30 {
		t.Errorf("claude-sonnet-4-5 cached = %v, want 0.30", row.CachedInputPer1m)
	}
	if row.CacheWriteInputPer1m == nil || *row.CacheWriteInputPer1m != 3.75 {
		t.Errorf("claude-sonnet-4-5 cache-write = %v, want 3.75", row.CacheWriteInputPer1m)
	}
}
