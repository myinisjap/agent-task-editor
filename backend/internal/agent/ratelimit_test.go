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
		{5, 10 * time.Minute}, // capped
		{10, 10 * time.Minute}, // still capped
	}
	for _, tc := range cases {
		got := BackoffDuration(tc.attempt)
		if got != tc.want {
			t.Errorf("BackoffDuration(%d) = %v, want %v", tc.attempt, got, tc.want)
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
