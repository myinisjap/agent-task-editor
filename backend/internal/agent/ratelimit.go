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

// BackoffDuration returns an exponential backoff for consecutive 429s:
// 30s * 2^attempt, capped at 10 minutes.
func BackoffDuration(attempt int) time.Duration {
	const base = 30 * time.Second
	const cap = 10 * time.Minute
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
	mu       sync.Mutex
	blocked  map[string]time.Time // agentConfigID → unblock time
	attempts map[string]int       // agentConfigID → consecutive 429 count
}

// NewRateLimitRegistry creates a new registry.
func NewRateLimitRegistry() *RateLimitRegistry {
	return &RateLimitRegistry{
		blocked:  make(map[string]time.Time),
		attempts: make(map[string]int),
	}
}

// Block marks agentConfigID as rate-limited until resetAt.
func (r *RateLimitRegistry) Block(agentConfigID string, resetAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blocked[agentConfigID] = resetAt
	r.attempts[agentConfigID]++
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

// Unblock clears rate-limit state for agentConfigID (call on successful dispatch).
func (r *RateLimitRegistry) Unblock(agentConfigID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.blocked, agentConfigID)
	delete(r.attempts, agentConfigID)
}
