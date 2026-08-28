// Package memguard is a last-resort safety net against a single agent run
// (or its subprocess tree — see #the-Setpgid-fix-in-providers/cli.go)
// exhausting the whole backend container's memory limit and starving the Go
// server itself (readyz timeouts, request handling stalls/500s). It watches
// cgroup v2 memory.current against memory.max on an interval and, if usage
// crosses a critical threshold, cancels the single oldest in-flight agent
// run — the best guess for a stuck/runaway one — via the same Pool.Cancel
// path a human or a run timeout already uses.
//
// This mirrors the scheduler pattern established by internal/worktreesweep
// and internal/logretention: a Guard.Run ticks on an interval until its
// context is cancelled, calling checkOnce (safe to call directly, e.g. from
// tests) each time.
package memguard

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Interval between memory checks. Deliberately short — the failure mode this
// guards against (a runaway subprocess tree) can exhaust a 4GB container in
// well under a minute.
const Interval = 5 * time.Second

// Threshold is the fraction of memory.max at which the guard cancels the
// oldest run. 90% leaves headroom for the Go server's own working set while
// still catching a runaway before it fully wedges the container.
const Threshold = 0.90

const (
	cgroupCurrentPath = "/sys/fs/cgroup/memory.current"
	cgroupMaxPath     = "/sys/fs/cgroup/memory.max"
)

// canceller is the subset of *agent.Pool the guard needs. Matched
// structurally (not imported) so this package never depends on agent,
// avoiding an import cycle risk as agent already depends on much of the
// rest of the backend.
type canceller interface {
	OldestRunID() string
	Cancel(runID string) bool
}

// Guard periodically checks container memory pressure and cancels the
// oldest in-flight run if it crosses Threshold.
type Guard struct {
	pool canceller
	// lastCancelAt debounces repeated cancellations: killing one run's
	// process tree doesn't free memory instantly (SIGKILL + OS reclaim), so
	// without this the guard could cancel every remaining run in rapid
	// succession before the first kill's memory is even reclaimed.
	lastCancelAt time.Time
}

// New creates a Guard for pool. Call Run to start watching.
func New(pool canceller) *Guard {
	return &Guard{pool: pool}
}

// Run ticks on Interval until ctx is cancelled, calling checkOnce each tick.
// A missing/unreadable cgroup file (non-Linux dev machine, no cgroup v2,
// unprivileged mount) is logged once and treated as "nothing to watch" —
// this guard is a container-only safety net, never a hard dependency.
func (g *Guard) Run(ctx context.Context) {
	if _, _, ok := readUsage(); !ok {
		slog.Info("memguard: cgroup v2 memory accounting unavailable, guard disabled")
		return
	}
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.checkOnce()
		}
	}
}

func (g *Guard) checkOnce() {
	current, max, ok := readUsage()
	if !ok || max <= 0 {
		return
	}
	g.cancelIfOverThreshold(current, max)
}

// cancelIfOverThreshold holds the guard's pure decision logic (ratio,
// debounce, victim selection) separated from cgroup file I/O so it can be
// exercised directly in tests via a fake canceller.
func (g *Guard) cancelIfOverThreshold(current, max int64) {
	ratio := float64(current) / float64(max)
	if ratio < Threshold {
		return
	}
	// Give a just-killed run's memory time to actually be reclaimed before
	// picking another victim.
	if time.Since(g.lastCancelAt) < Interval*2 {
		return
	}
	runID := g.pool.OldestRunID()
	if runID == "" {
		return
	}
	slog.Warn("memguard: container memory critical, cancelling oldest run",
		"component", "memguard", "run_id", runID, "mem_current", current, "mem_max", max, "ratio", ratio)
	g.lastCancelAt = time.Now()
	g.pool.Cancel(runID)
}

// readUsage reads cgroup v2 memory.current/memory.max. Returns ok=false if
// either file is missing, unreadable, or memory.max is "max" (unbounded —
// nothing to guard against).
func readUsage() (current, max int64, ok bool) {
	c, err := readInt(cgroupCurrentPath)
	if err != nil {
		return 0, 0, false
	}
	m, err := readInt(cgroupMaxPath)
	if err != nil {
		return 0, 0, false
	}
	return c, m, true
}

func readInt(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}
