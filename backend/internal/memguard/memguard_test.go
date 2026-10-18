package memguard

import (
	"os"
	"path/filepath"
	"testing"
)

type fakePool struct {
	oldestID   string
	cancelled  []string
	cancelFail bool
}

func (f *fakePool) OldestRunID() string { return f.oldestID }

func (f *fakePool) CancelForResourceConstraint(runID string) bool {
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

func TestReadUsage_SubtractsInactiveFileCache(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	orig := [3]string{cgroupCurrentPath, cgroupMaxPath, cgroupStatPath}
	t.Cleanup(func() { cgroupCurrentPath, cgroupMaxPath, cgroupStatPath = orig[0], orig[1], orig[2] })

	// 95% of max is in use, but most of it is reclaimable page cache: the
	// guard must not treat this as memory pressure.
	cgroupCurrentPath = write("memory.current", "950\n")
	cgroupMaxPath = write("memory.max", "1000\n")
	cgroupStatPath = write("memory.stat", "anon 300\nfile 640\nactive_file 40\ninactive_file 600\n")

	current, max, ok := readUsage()
	if !ok || current != 350 || max != 1000 {
		t.Fatalf("readUsage() = %d, %d, %v; want 350, 1000, true", current, max, ok)
	}

	pool := &fakePool{oldestID: "run-1"}
	New(pool).cancelIfOverThreshold(current, max)
	if len(pool.cancelled) != 0 {
		t.Fatalf("expected no cancellation when usage is mostly page cache, got %v", pool.cancelled)
	}

	// Without memory.stat, fall back to the raw memory.current figure.
	cgroupStatPath = filepath.Join(dir, "missing")
	if current, _, ok := readUsage(); !ok || current != 950 {
		t.Fatalf("readUsage() without memory.stat = %d, %v; want 950, true", current, ok)
	}
}

func TestWorkingSet_NeverNegative(t *testing.T) {
	if got := workingSet(100, 150); got != 0 {
		t.Fatalf("workingSet(100, 150) = %d, want 0", got)
	}
}
