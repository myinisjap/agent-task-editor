package ghsync

import (
	"testing"
	"time"
)

func TestBackoffActive_NilMap(t *testing.T) {
	s := &Syncer{}
	if s.backoffActive("repo1", time.Now()) {
		t.Error("expected backoffActive to be false with a nil backoff map")
	}
}

func TestRecordForgeFailure_ActivatesBackoff(t *testing.T) {
	s := &Syncer{interval: 30 * time.Second}
	now := time.Now()

	s.recordForgeFailure("repo1", now)

	if !s.backoffActive("repo1", now) {
		t.Fatal("expected repo1 to be in backoff immediately after a recorded failure")
	}
	failures, remaining := s.backoffStatus("repo1", now)
	if failures != 1 {
		t.Errorf("failures = %d, want 1", failures)
	}
	if remaining <= 0 {
		t.Errorf("remaining = %v, want > 0", remaining)
	}
}

func TestRecordForgeFailure_LengthensWithConsecutiveFailures(t *testing.T) {
	s := &Syncer{interval: 30 * time.Second}
	now := time.Now()

	s.recordForgeFailure("repo1", now)
	_, remaining1 := s.backoffStatus("repo1", now)

	// Simulate several more consecutive failures — the delay should grow
	// (until it hits maxBackoffDelay).
	for i := 0; i < 5; i++ {
		s.recordForgeFailure("repo1", now)
	}
	_, remaining6 := s.backoffStatus("repo1", now)

	if remaining6 <= remaining1 {
		t.Errorf("expected backoff delay to grow with consecutive failures: after 1 failure=%v, after 6=%v", remaining1, remaining6)
	}
}

func TestRecordForgeFailure_CapsAtMaxBackoffDelay(t *testing.T) {
	s := &Syncer{interval: 30 * time.Second}
	now := time.Now()

	// Enough consecutive failures to blow well past maxBackoffDelay if
	// uncapped.
	for i := 0; i < 50; i++ {
		s.recordForgeFailure("repo1", now)
	}
	_, remaining := s.backoffStatus("repo1", now)

	// Allow for the +/-10% jitter on top of maxBackoffDelay.
	upperBound := maxBackoffDelay + maxBackoffDelay/10 + time.Second
	if remaining > upperBound {
		t.Errorf("remaining = %v, want capped at roughly maxBackoffDelay (%v), upper bound %v", remaining, maxBackoffDelay, upperBound)
	}
}

func TestRecordForgeSuccess_ClearsBackoff(t *testing.T) {
	s := &Syncer{interval: 30 * time.Second}
	now := time.Now()

	s.recordForgeFailure("repo1", now)
	if !s.backoffActive("repo1", now) {
		t.Fatal("expected repo1 to be in backoff after a failure")
	}

	s.recordForgeSuccess("repo1")

	if s.backoffActive("repo1", now) {
		t.Error("expected backoff to be fully cleared after a recorded success")
	}
	failures, remaining := s.backoffStatus("repo1", now)
	if failures != 0 || remaining != 0 {
		t.Errorf("expected zeroed status after success, got failures=%d remaining=%v", failures, remaining)
	}
}

func TestBackoffActive_ExpiresAfterWindow(t *testing.T) {
	s := &Syncer{interval: 30 * time.Second}
	now := time.Now()

	s.recordForgeFailure("repo1", now)
	_, remaining := s.backoffStatus("repo1", now)

	later := now.Add(remaining + time.Second)
	if s.backoffActive("repo1", later) {
		t.Error("expected backoff to no longer be active once the window has elapsed")
	}
}

func TestRecordForgeFailure_ZeroInterval_NoOp(t *testing.T) {
	s := &Syncer{} // interval defaults to 0
	now := time.Now()

	s.recordForgeFailure("repo1", now)

	if s.backoffActive("repo1", now) {
		t.Error("expected no backoff to be recorded with a zero base interval")
	}
}

func TestBackoffStatus_UnknownRepo(t *testing.T) {
	s := &Syncer{interval: 30 * time.Second}
	s.recordForgeFailure("repo1", time.Now())

	failures, remaining := s.backoffStatus("repo2", time.Now())
	if failures != 0 || remaining != 0 {
		t.Errorf("expected zero status for an untracked repo, got failures=%d remaining=%v", failures, remaining)
	}
}
