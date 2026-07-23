package providers

import (
	"context"
	"os"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
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

// TestRunAccumulators_Attach_KnownModel verifies attach computes cost and
// leaves CostUnknown false when the model is known.
func TestRunAccumulators_Attach_KnownModel(t *testing.T) {
	acc := runAccumulators{model: "gpt-4o"}
	acc.addUsage(1_000_000, 1_000_000)

	var res agent.Result
	acc.attach(context.Background(), &res)

	want := 2.5 + 10.0
	if res.CostUSD != want {
		t.Errorf("want cost %v, got %v", want, res.CostUSD)
	}
	if res.CostUnknown {
		t.Error("expected CostUnknown=false for a known model")
	}
}

// TestRunAccumulators_Attach_UnknownModelWithTokens verifies attach flags
// CostUnknown when tokens were consumed but the model has no price entry,
// rather than silently reporting cost 0 indistinguishable from a free run.
func TestRunAccumulators_Attach_UnknownModelWithTokens(t *testing.T) {
	acc := runAccumulators{model: "some-totally-unknown-model"}
	acc.addUsage(1000, 1000)

	var res agent.Result
	acc.attach(context.Background(), &res)

	if res.CostUSD != 0 {
		t.Errorf("want cost 0 for unknown model, got %v", res.CostUSD)
	}
	if !res.CostUnknown {
		t.Error("expected CostUnknown=true when tokens were consumed but pricing is unknown")
	}
}

// TestRunAccumulators_Attach_UnknownModelNoTokens verifies a run that
// consumed no tokens at all is never flagged CostUnknown even for an
// unpriced model — there is nothing to have gotten a real estimate for.
func TestRunAccumulators_Attach_UnknownModelNoTokens(t *testing.T) {
	acc := runAccumulators{model: "some-totally-unknown-model"}

	var res agent.Result
	acc.attach(context.Background(), &res)

	if res.CostUnknown {
		t.Error("expected CostUnknown=false when no tokens were consumed")
	}
}

// TestRunAccumulators_Attach_UsesPriceResolver verifies attach uses the
// accumulator's priceResolver (not just the hardcoded map) when set.
func TestRunAccumulators_Attach_UsesPriceResolver(t *testing.T) {
	acc := runAccumulators{
		model:         "anything",
		priceResolver: fakePriceResolver{inPer1M: 4, outPer1M: 8, known: true},
	}
	acc.addUsage(1_000_000, 1_000_000)

	var res agent.Result
	acc.attach(context.Background(), &res)

	want := 4.0 + 8.0
	if res.CostUSD != want {
		t.Errorf("want cost %v, got %v", want, res.CostUSD)
	}
	if res.CostUnknown {
		t.Error("expected CostUnknown=false when the resolver reports known=true")
	}
}
