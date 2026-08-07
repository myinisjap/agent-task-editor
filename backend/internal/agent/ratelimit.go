package agent

import (
	"fmt"
	"sync"
	"time"
)

// ErrRateLimit is returned by providers when the upstream API responds with 429.
type ErrRateLimit struct {
	// ResetAt is the time at which the rate limit resets. Zero means unknown — use backoff.
	ResetAt time.Time
	Message string
}

func (e *ErrRateLimit) Error() string {
	return fmt.Sprintf("rate limited: %s", e.Message)
}

// Transient implements the transientErr marker interface — rate limits are
// always treated as transient for retry-budget purposes.
func (e *ErrRateLimit) Transient() bool { return true }

// BackoffDuration returns an exponential backoff for consecutive 429s:
// 30s * 2^attempt, capped at 10 minutes.
func BackoffDuration(attempt int) time.Duration {
	return BackoffDurationWithBase(attempt, 30*time.Second)
}

// BackoffDurationWithBase returns an exponential backoff of base * 2^attempt,
// capped at 10 minutes. Used by the per-task transient-retry policy, where
// the base is configurable per agent config (AgentConfig.RetryBackoffSecs),
// as well as by BackoffDuration for the fixed 30s rate-limit case.
func BackoffDurationWithBase(attempt int, base time.Duration) time.Duration {
	const cap = 10 * time.Minute
	if base <= 0 {
		base = 30 * time.Second
	}
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
		if d > cap {
			return cap
		}
	}
	return d
}

// RateLimitRegistry tracks per-agent-config rate-limit blocks concurrency-safely.
type RateLimitRegistry struct {
	mu        sync.Mutex
	blocked   map[string]time.Time // agentConfigID → unblock time
	attempts  map[string]int       // agentConfigID → consecutive 429 count
	blockedAt map[string]time.Time // agentConfigID → wall-clock time of most recent Block/BlockWithBackoff
}

// NewRateLimitRegistry creates a new registry.
func NewRateLimitRegistry() *RateLimitRegistry {
	return &RateLimitRegistry{
		blocked:   make(map[string]time.Time),
		attempts:  make(map[string]int),
		blockedAt: make(map[string]time.Time),
	}
}

// Block marks agentConfigID as rate-limited until resetAt.
func (r *RateLimitRegistry) Block(agentConfigID string, resetAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blocked[agentConfigID] = resetAt
	r.attempts[agentConfigID]++
	r.blockedAt[agentConfigID] = time.Now()
}

// BlockWithBackoff marks agentConfigID as rate-limited using exponential backoff
// based on the number of consecutive 429s seen.
func (r *RateLimitRegistry) BlockWithBackoff(agentConfigID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	attempt := r.attempts[agentConfigID]
	d := BackoffDuration(attempt)
	r.blocked[agentConfigID] = time.Now().Add(d)
	r.attempts[agentConfigID] = attempt + 1
	r.blockedAt[agentConfigID] = time.Now()
}

// IsBlocked returns (true, unblockTime) if agentConfigID is currently rate-limited.
// Expired entries are auto-cleared and return (false, zero).
func (r *RateLimitRegistry) IsBlocked(agentConfigID string) (bool, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	until, ok := r.blocked[agentConfigID]
	if !ok {
		return false, time.Time{}
	}
	if time.Now().After(until) {
		delete(r.blocked, agentConfigID)
		return false, time.Time{}
	}
	return true, until
}

// BlockedUntil returns the unblock time for agentConfigID (zero time if not blocked).
func (r *RateLimitRegistry) BlockedUntil(agentConfigID string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.blocked[agentConfigID]
}

// Unblock unconditionally clears rate-limit state for agentConfigID. Prefer
// UnblockIfNotBlockedSince for in-run callers (e.g. pool.go's post-run
// cleanup), since an unconditional clear can wipe a block registered a moment
// ago by a concurrently-finishing sibling run against the same agent config.
func (r *RateLimitRegistry) Unblock(agentConfigID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.blocked, agentConfigID)
	delete(r.attempts, agentConfigID)
	delete(r.blockedAt, agentConfigID)
}

// UnblockIfNotBlockedSince clears rate-limit state for agentConfigID only if no
// Block/BlockWithBackoff landed at or after `since` (the calling run's start
// time). With MAX_WORKERS > 1 several runs share one agent config; a run that
// started before a sibling run's 429 must not wipe that sibling's fresh block
// (nor reset the consecutive-429 `attempts` counter that drives
// BlockWithBackoff's escalating ladder). Returns true if it cleared.
func (r *RateLimitRegistry) UnblockIfNotBlockedSince(agentConfigID string, since time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !since.IsZero() {
		if at, ok := r.blockedAt[agentConfigID]; ok && !at.Before(since) {
			// A block landed at or after this run started — preserve it (and its
			// attempts counter) rather than clobbering a sibling's fresh 429.
			return false
		}
	}
	delete(r.blocked, agentConfigID)
	delete(r.attempts, agentConfigID)
	delete(r.blockedAt, agentConfigID)
	return true
}
