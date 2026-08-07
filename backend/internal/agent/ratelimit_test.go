package agent

import (
	"testing"
	"time"
)

func TestBackoffDuration(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 30 * time.Second},
		{1, 60 * time.Second},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{4, 8 * time.Minute},
		{5, 10 * time.Minute},  // capped
		{10, 10 * time.Minute}, // still capped
	}
	for _, tc := range cases {
		got := BackoffDuration(tc.attempt)
		if got != tc.want {
			t.Errorf("BackoffDuration(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestBackoffDurationWithBase(t *testing.T) {
	cases := []struct {
		attempt int
		base    time.Duration
		want    time.Duration
	}{
		{0, 10 * time.Second, 10 * time.Second},
		{1, 10 * time.Second, 20 * time.Second},
		{2, 10 * time.Second, 40 * time.Second},
		{0, 5 * time.Minute, 5 * time.Minute},
		{1, 5 * time.Minute, 10 * time.Minute}, // capped
		{5, 5 * time.Minute, 10 * time.Minute}, // still capped
		{0, 0, 30 * time.Second},               // non-positive base falls back to 30s default
	}
	for _, tc := range cases {
		got := BackoffDurationWithBase(tc.attempt, tc.base)
		if got != tc.want {
			t.Errorf("BackoffDurationWithBase(%d, %v) = %v, want %v", tc.attempt, tc.base, got, tc.want)
		}
	}
}

func TestRateLimitRegistry_NotBlocked(t *testing.T) {
	r := NewRateLimitRegistry()
	blocked, _ := r.IsBlocked("cfg-1")
	if blocked {
		t.Error("expected not blocked for unknown config")
	}
}

func TestRateLimitRegistry_Block(t *testing.T) {
	r := NewRateLimitRegistry()
	future := time.Now().Add(5 * time.Minute)
	r.Block("cfg-1", future)
	blocked, until := r.IsBlocked("cfg-1")
	if !blocked {
		t.Error("expected blocked after Block()")
	}
	if until.IsZero() {
		t.Error("expected non-zero unblock time")
	}
}

func TestRateLimitRegistry_BlockExpired(t *testing.T) {
	r := NewRateLimitRegistry()
	past := time.Now().Add(-1 * time.Second)
	r.Block("cfg-1", past)
	blocked, _ := r.IsBlocked("cfg-1")
	if blocked {
		t.Error("expected not blocked after expiry")
	}
}

func TestRateLimitRegistry_BlockWithBackoff(t *testing.T) {
	r := NewRateLimitRegistry()
	r.BlockWithBackoff("cfg-1")
	blocked, until := r.IsBlocked("cfg-1")
	if !blocked {
		t.Error("expected blocked after BlockWithBackoff()")
	}
	// First attempt: 30s backoff — unblock time should be ~30s from now
	expected := time.Now().Add(29 * time.Second) // slight tolerance
	if until.Before(expected) {
		t.Errorf("expected unblock time >= %v, got %v", expected, until)
	}
}

func TestRateLimitRegistry_BlockWithBackoffIncreases(t *testing.T) {
	r := NewRateLimitRegistry()
	// Manually set attempt count to 1 by calling Block first
	r.BlockWithBackoff("cfg-1") // attempt 0 → 30s, sets attempts to 1
	// Force-expire the first block
	r.mu.Lock()
	r.blocked["cfg-1"] = time.Now().Add(-1 * time.Second)
	r.mu.Unlock()

	r.BlockWithBackoff("cfg-1") // attempt 1 → 60s
	_, until := r.IsBlocked("cfg-1")
	expected := time.Now().Add(59 * time.Second)
	if until.Before(expected) {
		t.Errorf("second backoff should be ~60s, unblock=%v, expected >= %v", until, expected)
	}
}

func TestRateLimitRegistry_Unblock(t *testing.T) {
	r := NewRateLimitRegistry()
	r.Block("cfg-1", time.Now().Add(5*time.Minute))
	r.Unblock("cfg-1")
	blocked, _ := r.IsBlocked("cfg-1")
	if blocked {
		t.Error("expected not blocked after Unblock()")
	}
	// Verify attempt count is reset
	r.BlockWithBackoff("cfg-1")
	_, until := r.IsBlocked("cfg-1")
	// After Unblock, attempt count resets to 0, so first backoff is 30s
	expected := time.Now().Add(29 * time.Second)
	if until.Before(expected) {
		t.Errorf("after Unblock, attempt should reset: unblock=%v, expected >= %v", until, expected)
	}
}

func TestRateLimitRegistry_UnblockIfNotBlockedSince_PreservesNewerBlock(t *testing.T) {
	r := NewRateLimitRegistry()
	since := time.Now()
	time.Sleep(2 * time.Millisecond)
	// A sibling run registers a fresh 429 after `since`.
	r.Block("cfg-1", time.Now().Add(5*time.Minute))

	cleared := r.UnblockIfNotBlockedSince("cfg-1", since)
	if cleared {
		t.Error("expected UnblockIfNotBlockedSince to be a no-op when a newer block exists")
	}
	blocked, _ := r.IsBlocked("cfg-1")
	if !blocked {
		t.Error("expected block to survive UnblockIfNotBlockedSince")
	}

	// attempts must be preserved (not reset), so the backoff ladder escalates
	// on the next 429 rather than resetting to the 30s floor.
	r.mu.Lock()
	r.blocked["cfg-1"] = time.Now().Add(-1 * time.Second) // force-expire so BlockWithBackoff logs another attempt
	r.mu.Unlock()
	r.BlockWithBackoff("cfg-1")
	_, until := r.IsBlocked("cfg-1")
	expected := time.Now().Add(59 * time.Second) // second attempt → ~60s, not 30s
	if until.Before(expected) {
		t.Errorf("expected backoff ladder to escalate (attempts preserved): unblock=%v, expected >= %v", until, expected)
	}
}

func TestRateLimitRegistry_UnblockIfNotBlockedSince_ClearsOlderBlock(t *testing.T) {
	r := NewRateLimitRegistry()
	r.Block("cfg-1", time.Now().Add(5*time.Minute))
	time.Sleep(2 * time.Millisecond)
	since := time.Now()

	cleared := r.UnblockIfNotBlockedSince("cfg-1", since)
	if !cleared {
		t.Error("expected UnblockIfNotBlockedSince to clear a block registered before `since`")
	}
	blocked, _ := r.IsBlocked("cfg-1")
	if blocked {
		t.Error("expected not blocked after UnblockIfNotBlockedSince clears an older block")
	}

	// attempts should be reset, so next backoff is back at the 30s floor.
	r.BlockWithBackoff("cfg-1")
	_, until := r.IsBlocked("cfg-1")
	expected := time.Now().Add(29 * time.Second)
	if until.Before(expected) {
		t.Errorf("expected attempts reset after clear: unblock=%v, expected >= %v", until, expected)
	}
}

func TestRateLimitRegistry_UnblockIfNotBlockedSince_NoBlockAtAll(t *testing.T) {
	r := NewRateLimitRegistry()
	if cleared := r.UnblockIfNotBlockedSince("cfg-1", time.Now()); !cleared {
		t.Error("expected UnblockIfNotBlockedSince to report cleared=true when there was nothing to clear")
	}
}

func TestRateLimitRegistry_UnblockIfNotBlockedSince_ZeroSinceAlwaysClears(t *testing.T) {
	r := NewRateLimitRegistry()
	r.Block("cfg-1", time.Now().Add(5*time.Minute))
	if cleared := r.UnblockIfNotBlockedSince("cfg-1", time.Time{}); !cleared {
		t.Error("expected zero `since` to behave like unconditional Unblock")
	}
	blocked, _ := r.IsBlocked("cfg-1")
	if blocked {
		t.Error("expected not blocked after zero-since UnblockIfNotBlockedSince")
	}
}

func TestRateLimitRegistry_BlockedUntil(t *testing.T) {
	r := NewRateLimitRegistry()
	if !r.BlockedUntil("cfg-1").IsZero() {
		t.Error("expected zero time for unknown config")
	}
	future := time.Now().Add(5 * time.Minute)
	r.Block("cfg-1", future)
	got := r.BlockedUntil("cfg-1")
	if got.IsZero() {
		t.Error("expected non-zero blocked-until time")
	}
}

func TestErrRateLimit_ErrorAndTransient(t *testing.T) {
	err := &ErrRateLimit{Message: "too many requests", ResetAt: time.Now().Add(time.Minute)}
	if got, want := err.Error(), "rate limited: too many requests"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !err.Transient() {
		t.Errorf("Transient() = false, want true")
	}
}
