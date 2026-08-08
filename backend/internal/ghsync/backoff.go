package ghsync

import (
	"math/rand"
	"time"
)

// maxBackoffShift caps how many times the base interval is doubled
// (2^maxBackoffShift), so a repo that's been failing for a long time doesn't
// compute an ever-growing exponent — the delay itself is separately capped
// at maxBackoffDelay below, but this keeps the shift computation itself
// bounded regardless of how large failures grows.
const maxBackoffShift = 10

// maxBackoffDelay is the longest a repo's effective poll interval can
// lengthen to under consecutive forge-call failures, regardless of how many
// consecutive failures have been recorded.
const maxBackoffDelay = 15 * time.Minute

// repoBackoff tracks a single repo's consecutive forge-call failure state.
// Purely in-memory / best-effort: this is transient operational state (not
// worth a schema change), and resets on process restart.
type repoBackoff struct {
	failures  int
	nextTryAt time.Time
}

// backoffActive reports whether repoID is currently within its backoff
// window (i.e. should be skipped this sweep). Safe to call on a nil
// s.backoff map (every ghsync test builds Syncer literals directly,
// bypassing New).
func (s *Syncer) backoffActive(repoID string, now time.Time) bool {
	s.backoffMu.Lock()
	defer s.backoffMu.Unlock()
	if s.backoff == nil {
		return false
	}
	b, ok := s.backoff[repoID]
	if !ok {
		return false
	}
	return now.Before(b.nextTryAt)
}

// backoffStatus returns repoID's current consecutive-failure count and how
// long remains until its backoff window ends (0 if not currently backed
// off), for logging.
func (s *Syncer) backoffStatus(repoID string, now time.Time) (failures int, remaining time.Duration) {
	s.backoffMu.Lock()
	defer s.backoffMu.Unlock()
	if s.backoff == nil {
		return 0, 0
	}
	b, ok := s.backoff[repoID]
	if !ok {
		return 0, 0
	}
	remaining = b.nextTryAt.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	return b.failures, remaining
}

// recordForgeFailure records a forge-call failure for repoID, lengthening
// its effective poll interval: delay = s.interval * 2^min(failures-1,
// maxBackoffShift), capped at maxBackoffDelay, plus up to +/-10% jitter so
// many repos failing together (e.g. a shared rate limit) don't
// re-synchronise into a thundering herd once the window ends.
//
// At most one failure should be recorded per repo per sweep — sweep dedupes
// this via a per-sweep set (see sweep's recordedThisSweep), since a repo with
// many eligible tasks would otherwise hit the backoff cap from a single bad
// sweep.
func (s *Syncer) recordForgeFailure(repoID string, now time.Time) {
	if s.interval <= 0 {
		return // avoid a zero/negative base interval producing a zero delay
	}
	s.backoffMu.Lock()
	defer s.backoffMu.Unlock()
	if s.backoff == nil {
		s.backoff = map[string]*repoBackoff{}
	}
	b, ok := s.backoff[repoID]
	if !ok {
		b = &repoBackoff{}
		s.backoff[repoID] = b
	}
	b.failures++

	shift := b.failures - 1
	if shift > maxBackoffShift {
		shift = maxBackoffShift
	}
	delay := s.interval * time.Duration(int64(1)<<uint(shift))
	if delay > maxBackoffDelay || delay <= 0 {
		delay = maxBackoffDelay
	}

	// +/-10% jitter.
	jitterRange := float64(delay) * 0.1
	jitter := time.Duration(jitterRange*2*rand.Float64() - jitterRange)
	delay += jitter
	if delay < 0 {
		delay = 0
	}

	b.nextTryAt = now.Add(delay)
}

// recordForgeSuccess clears repoID's backoff state entirely (full reset), so
// a repo that recovers goes straight back to the normal poll interval rather
// than ramping back down gradually.
func (s *Syncer) recordForgeSuccess(repoID string) {
	s.backoffMu.Lock()
	defer s.backoffMu.Unlock()
	if s.backoff == nil {
		return
	}
	delete(s.backoff, repoID)
}
