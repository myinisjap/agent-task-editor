package agent

import (
	"sync"
	"time"
)

// Memory-pressure tunables. Package vars (not config) so tests can adjust
// them, matching memguard's hard-coded thresholds.
var (
	// memCooldownBase is the cooldown after a single memory-guard cancel;
	// it doubles on each back-to-back cancel of the same task.
	memCooldownBase = 60 * time.Second
	// memCooldownMax caps the per-task cooldown.
	memCooldownMax = 5 * time.Minute
	// memStreakWindow: a cancel within this gap of the same task's previous
	// memory-guard cancel extends the streak; otherwise it resets.
	memStreakWindow = 15 * time.Minute
	// memBurstThreshold cancels (any tasks) within memBurstWindow pause all
	// new dispatch for memPauseDuration.
	memBurstThreshold = 3
	memBurstWindow    = 3 * time.Minute
	memPauseDuration  = 60 * time.Second
)

type memStreak struct {
	count  int
	lastAt time.Time
}

// memPressureTracker throttles re-dispatch after memory-guard cancels
// (issues #532/#540: tight retry storms into the same memory pressure).
// The per-task cooldown is persisted by the caller in tasks.next_retry_at
// (no sleeping); only the growth streak and the global pause live here, in
// memory. The zero value is ready to use.
type memPressureTracker struct {
	mu          sync.Mutex
	streaks     map[string]memStreak
	recent      []time.Time
	pausedUntil time.Time
	now         func() time.Time
}

func (t *memPressureTracker) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// memCooldown returns the cooldown for the given streak (1-based).
func memCooldown(streak int) time.Duration {
	if streak < 1 {
		streak = 1
	}
	d := BackoffDurationWithBase(streak-1, memCooldownBase)
	if d > memCooldownMax {
		d = memCooldownMax
	}
	return d
}

// recordCancel records a memory-guard cancel for taskID and returns the
// cooldown to apply, the task's streak, and whether this cancel tripped the
// global dispatch pause (with its end time).
func (t *memPressureTracker) recordCancel(taskID string) (cooldown time.Duration, streak int, tripped bool, pausedUntil time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock()

	if t.streaks == nil {
		t.streaks = make(map[string]memStreak)
	}
	for id, s := range t.streaks {
		if now.Sub(s.lastAt) >= memStreakWindow {
			delete(t.streaks, id)
		}
	}
	streak = t.streaks[taskID].count + 1
	t.streaks[taskID] = memStreak{count: streak, lastAt: now}
	cooldown = memCooldown(streak)

	kept := t.recent[:0]
	for _, ts := range t.recent {
		if now.Sub(ts) < memBurstWindow {
			kept = append(kept, ts)
		}
	}
	t.recent = append(kept, now)
	if len(t.recent) >= memBurstThreshold {
		until := now.Add(memPauseDuration)
		if until.After(t.pausedUntil) {
			t.pausedUntil = until
		}
		t.recent = nil
		tripped = true
	}
	return cooldown, streak, tripped, t.pausedUntil
}

// pausedUntilTime reports the pause end and whether it is currently active.
func (t *memPressureTracker) pausedUntilTime() (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pausedUntil, t.clock().Before(t.pausedUntil)
}
