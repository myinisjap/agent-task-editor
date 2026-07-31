package providers

import (
	"context"
	"testing"
)

// TestCostWatchdog_InactiveWithNoBudget verifies a watchdog with budget<=0
// never fires, regardless of usage — the mid-run kill switch must be fully
// inert when no cost cap is configured.
func TestCostWatchdog_InactiveWithNoBudget(t *testing.T) {
	w := newCostWatchdog(0, 0, 0.8, fakePriceResolver{inPer1M: 1000, outPer1M: 1000, known: true}, "gpt-4o")
	if w.active() {
		t.Fatal("expected watchdog to be inactive with budget=0")
	}
	projected, warn, exceeded := w.observe(context.Background(), 1_000_000, 1_000_000)
	if projected != 0 || warn || exceeded {
		t.Errorf("expected (0, false, false) with no budget, got (%v, %v, %v)", projected, warn, exceeded)
	}
}

// TestCostWatchdog_UnknownModelNeverFires verifies a model the resolver
// doesn't recognize is treated as un-projectable rather than defaulting to
// a $0 (or fabricated) cost that could cause a false-positive kill.
func TestCostWatchdog_UnknownModelNeverFires(t *testing.T) {
	w := newCostWatchdog(10, 0, 0.8, fakePriceResolver{known: false}, "mystery-model")
	projected, warn, exceeded := w.observe(context.Background(), 1_000_000_000, 1_000_000_000)
	if projected != 0 || warn || exceeded {
		t.Errorf("expected (0, false, false) for an unpriced model, got (%v, %v, %v)", projected, warn, exceeded)
	}
}

// TestCostWatchdog_WarnFiresOnceAtThreshold verifies the warning fires the
// first time projected cost crosses warnRatio*budget, and never fires again
// on subsequent observations even if cost keeps climbing.
func TestCostWatchdog_WarnFiresOnceAtThreshold(t *testing.T) {
	// $10/1M input tokens, $0/1M output -> 1,000,000 input tokens = $10.
	resolver := fakePriceResolver{inPer1M: 10, outPer1M: 0, known: true}
	w := newCostWatchdog(10, 0, 0.8, resolver, "test-model")

	// 750,000 tokens = $7.50 = 75% of budget -- under the 80% warn line.
	if _, warn, exceeded := w.observe(context.Background(), 750_000, 0); warn || exceeded {
		t.Fatalf("expected no warn/exceeded at 75%% of budget, got warn=%v exceeded=%v", warn, exceeded)
	}

	// 850,000 tokens = $8.50 = 85% of budget -- crosses the 80% warn line.
	projected, warn, exceeded := w.observe(context.Background(), 850_000, 0)
	if !warn {
		t.Error("expected warn=true crossing the 80% threshold")
	}
	if exceeded {
		t.Error("expected exceeded=false at 85% of budget")
	}
	if projected != 8.5 {
		t.Errorf("expected projected cost 8.5, got %v", projected)
	}

	// A subsequent observation still above the warn line must not warn again.
	if _, warn, _ := w.observe(context.Background(), 900_000, 0); warn {
		t.Error("expected warn=false on a second observation past the threshold (one-shot)")
	}
}

// TestCostWatchdog_ExceededAtBudget verifies exceeded=true once projected
// cost reaches or crosses the budget itself (not just the warn ratio).
func TestCostWatchdog_ExceededAtBudget(t *testing.T) {
	resolver := fakePriceResolver{inPer1M: 10, outPer1M: 0, known: true}
	w := newCostWatchdog(10, 0, 0.8, resolver, "test-model")

	// 1,000,000 tokens = $10 = exactly the budget.
	projected, warn, exceeded := w.observe(context.Background(), 1_000_000, 0)
	if !exceeded {
		t.Error("expected exceeded=true at exactly the budget")
	}
	if !warn {
		t.Error("expected warn=true too, since exceeding the budget also crosses the warn ratio")
	}
	if projected != 10 {
		t.Errorf("expected projected cost 10, got %v", projected)
	}
}

// TestCostWatchdog_PriorSpendCountsTowardBudget verifies the watchdog
// projects *task* cost (prior runs' recorded spend plus this run's
// incremental usage), not just this run's own cost in isolation -- a task
// that already spent most of its budget on earlier runs should trip the
// kill switch quickly on this run.
func TestCostWatchdog_PriorSpendCountsTowardBudget(t *testing.T) {
	resolver := fakePriceResolver{inPer1M: 10, outPer1M: 0, known: true}
	// Budget 10, already spent 9 from prior runs -- only $1 of headroom left.
	w := newCostWatchdog(10, 9, 0.8, resolver, "test-model")

	// 50,000 tokens = $0.50 incremental; 9 + 0.5 = 9.5, still under budget
	// but already past the 80% warn line (thanks to prior spend).
	projected, warn, exceeded := w.observe(context.Background(), 50_000, 0)
	if projected != 9.5 {
		t.Errorf("expected projected cost 9.5 (prior 9 + incremental 0.5), got %v", projected)
	}
	if !warn {
		t.Error("expected warn=true given prior spend alone is already past the warn ratio")
	}
	if exceeded {
		t.Error("expected exceeded=false at 9.5 of a 10 budget")
	}

	// 60,000 tokens = $0.60 incremental; 9 + 0.6 = 9.6, still under 10.
	// 100,000 tokens = $1.00 incremental; 9 + 1.00 = 10.00 -- now exceeded.
	if _, _, exceeded := w.observe(context.Background(), 100_000, 0); !exceeded {
		t.Error("expected exceeded=true once prior spend + incremental usage reaches the budget")
	}
}

// TestCostWatchdog_WarnRatioDisabled verifies warnRatio<=0 disables only the
// early warning, leaving the hard kill switch active.
func TestCostWatchdog_WarnRatioDisabled(t *testing.T) {
	resolver := fakePriceResolver{inPer1M: 10, outPer1M: 0, known: true}
	w := newCostWatchdog(10, 0, 0, resolver, "test-model")

	if _, warn, exceeded := w.observe(context.Background(), 1_000_000, 0); warn || !exceeded {
		t.Errorf("expected (warn=false, exceeded=true) with warnRatio disabled, got warn=%v exceeded=%v", warn, exceeded)
	}
}
