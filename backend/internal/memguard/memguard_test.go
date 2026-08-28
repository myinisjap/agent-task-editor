package memguard

import "testing"

type fakePool struct {
	oldestID   string
	cancelled  []string
	cancelFail bool
}

func (f *fakePool) OldestRunID() string { return f.oldestID }

func (f *fakePool) Cancel(runID string) bool {
	if f.cancelFail {
		return false
	}
	f.cancelled = append(f.cancelled, runID)
	return true
}

func TestGuard_CancelsOldestRun_WhenOverThreshold(t *testing.T) {
	pool := &fakePool{oldestID: "run-1"}
	g := New(pool)

	g.cancelIfOverThreshold(950, 1000) // 95% > 90% threshold

	if len(pool.cancelled) != 1 || pool.cancelled[0] != "run-1" {
		t.Fatalf("expected run-1 to be cancelled, got %v", pool.cancelled)
	}
}

func TestGuard_DoesNothing_UnderThreshold(t *testing.T) {
	pool := &fakePool{oldestID: "run-1"}
	g := New(pool)

	g.cancelIfOverThreshold(500, 1000) // 50% < 90% threshold

	if len(pool.cancelled) != 0 {
		t.Fatalf("expected no cancellation under threshold, got %v", pool.cancelled)
	}
}

func TestGuard_Debounces_RepeatedCrossings(t *testing.T) {
	pool := &fakePool{oldestID: "run-1"}
	g := New(pool)

	g.cancelIfOverThreshold(950, 1000)
	g.cancelIfOverThreshold(950, 1000) // immediately after — should be debounced

	if len(pool.cancelled) != 1 {
		t.Fatalf("expected exactly one cancellation within the debounce window, got %d", len(pool.cancelled))
	}
}

func TestGuard_NoOp_WhenNothingRunning(t *testing.T) {
	pool := &fakePool{oldestID: ""}
	g := New(pool)

	g.cancelIfOverThreshold(950, 1000)

	if len(pool.cancelled) != 0 {
		t.Fatalf("expected no cancellation with no in-flight run, got %v", pool.cancelled)
	}
}
