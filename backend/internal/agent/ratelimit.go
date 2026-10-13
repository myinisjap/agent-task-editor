package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
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
	store     RateLimitStore       // optional write-through persistence; nil = in-memory only
}

// RateLimitStore persists registry blocks so they survive a restart. All
// methods are best-effort from the registry's point of view: errors are
// logged and never affect dispatch.
type RateLimitStore interface {
	Save(ctx context.Context, agentConfigID string, until time.Time, attempts int) error
	Delete(ctx context.Context, agentConfigID string) error
	// LoadActive returns blocks whose unblock time is after now.
	LoadActive(ctx context.Context, now time.Time) ([]PersistedRateLimit, error)
	// PruneExpired removes rows whose unblock time is at or before now.
	PruneExpired(ctx context.Context, now time.Time) error
}

// PersistedRateLimit is one stored block.
type PersistedRateLimit struct {
	AgentConfigID string
	Until         time.Time
	Attempts      int
}

const rateLimitStoreTimeout = 5 * time.Second

// SQLRateLimitStore is the sqlite-backed RateLimitStore.
type SQLRateLimitStore struct{ q *gen.Queries }

// NewSQLRateLimitStore wraps sqlc queries as a RateLimitStore.
func NewSQLRateLimitStore(q *gen.Queries) *SQLRateLimitStore { return &SQLRateLimitStore{q: q} }

func (s *SQLRateLimitStore) Save(ctx context.Context, id string, until time.Time, attempts int) error {
	return s.q.UpsertAgentRateLimit(ctx, gen.UpsertAgentRateLimitParams{
		AgentConfigID: id, BlockedUntil: until, Attempts: int64(attempts),
	})
}

func (s *SQLRateLimitStore) Delete(ctx context.Context, id string) error {
	return s.q.DeleteAgentRateLimit(ctx, id)
}

func (s *SQLRateLimitStore) LoadActive(ctx context.Context, now time.Time) ([]PersistedRateLimit, error) {
	rows, err := s.q.ListActiveAgentRateLimits(ctx, now)
	if err != nil {
		return nil, err
	}
	out := make([]PersistedRateLimit, 0, len(rows))
	for _, r := range rows {
		out = append(out, PersistedRateLimit{AgentConfigID: r.AgentConfigID, Until: r.BlockedUntil, Attempts: int(r.Attempts)})
	}
	return out, nil
}

func (s *SQLRateLimitStore) PruneExpired(ctx context.Context, now time.Time) error {
	_, err := s.q.PruneExpiredAgentRateLimits(ctx, now)
	return err
}

// NewRateLimitRegistryWithStore creates a registry backed by store: expired
// rows are pruned, unexpired ones are loaded, and every later Block/Unblock
// is written through. A nil store behaves like NewRateLimitRegistry.
func NewRateLimitRegistryWithStore(store RateLimitStore) *RateLimitRegistry {
	r := NewRateLimitRegistry()
	if store == nil {
		return r
	}
	r.store = store
	ctx, cancel := context.WithTimeout(context.Background(), rateLimitStoreTimeout)
	defer cancel()
	now := time.Now()
	if err := store.PruneExpired(ctx, now); err != nil {
		slog.Warn("rate limit registry: prune expired failed", "err", err)
	}
	rows, err := store.LoadActive(ctx, now)
	if err != nil {
		slog.Warn("rate limit registry: load persisted blocks failed", "err", err)
		return r
	}
	for _, row := range rows {
		if !row.Until.After(now) {
			continue
		}
		r.blocked[row.AgentConfigID] = row.Until
		r.attempts[row.AgentConfigID] = row.Attempts
	}
	if len(rows) > 0 {
		slog.Info("rate limit registry: restored blocks", "count", len(rows))
	}
	return r
}

func (r *RateLimitRegistry) persistSave(id string, until time.Time, attempts int) {
	if r.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), rateLimitStoreTimeout)
	defer cancel()
	if err := r.store.Save(ctx, id, until, attempts); err != nil {
		slog.Warn("rate limit registry: persist block failed", "agent_config_id", id, "err", err)
	}
}

func (r *RateLimitRegistry) persistDelete(id string) {
	if r.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), rateLimitStoreTimeout)
	defer cancel()
	if err := r.store.Delete(ctx, id); err != nil {
		slog.Warn("rate limit registry: clear persisted block failed", "agent_config_id", id, "err", err)
	}
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
	r.persistSave(agentConfigID, resetAt, r.attempts[agentConfigID])
}

// BlockWithBackoff marks agentConfigID as rate-limited using exponential backoff
// based on the number of consecutive 429s seen.
func (r *RateLimitRegistry) BlockWithBackoff(agentConfigID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	attempt := r.attempts[agentConfigID]
	d := BackoffDuration(attempt)
	until := time.Now().Add(d)
	r.blocked[agentConfigID] = until
	r.attempts[agentConfigID] = attempt + 1
	r.blockedAt[agentConfigID] = time.Now()
	r.persistSave(agentConfigID, until, attempt+1)
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
	r.persistDelete(agentConfigID)
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
	r.persistDelete(agentConfigID)
	return true
}
