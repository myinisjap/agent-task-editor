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

// RateLimitRegistry tracks rate-limit blocks concurrency-safely. Keys are
// usage pools (provider accounts, see usagepool.go), not agent config ids;
// the persisted agent_rate_limits.agent_config_id column holds that key.
type RateLimitRegistry struct {
	mu        sync.Mutex
	blocked   map[string]time.Time // agentConfigID → unblock time
	attempts  map[string]int       // agentConfigID → consecutive 429 count
	blockedAt map[string]time.Time // agentConfigID → wall-clock time of most recent Block/BlockWithBackoff
	store     RateLimitStore       // optional write-through persistence; nil = in-memory only

	// resetChecks is set once a ResetChecker is running. Limits with no
	// known reset time are then handed to it (BlockUnknownReset) instead of
	// the blind backoff ladder.
	resetChecks bool
	probes      map[string]*resetCheckState // pool → pending reset check
}

// resetCheckState is a pool whose reset time is unknown and is being found
// by probing its CLI (see ResetChecker).
type resetCheckState struct {
	cfg     AgentConfig // the config whose run hit the limit; probes run as it
	attempt int         // failed probes so far
	next    time.Time   // when the next probe is due
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
		probes:    make(map[string]*resetCheckState),
	}
}

// Block marks agentConfigID as rate-limited until resetAt.
func (r *RateLimitRegistry) Block(agentConfigID string, resetAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blocked[agentConfigID] = resetAt
	r.attempts[agentConfigID]++
	r.blockedAt[agentConfigID] = time.Now()
	delete(r.probes, agentConfigID) // the reset time is known now
	r.persistSave(agentConfigID, resetAt, r.attempts[agentConfigID])
}

// resetCheckDelays is how long the ResetChecker waits before each probe of a
// pool with an unknown reset time: soon at first (short-window limits clear
// within minutes), then settling at every 15 minutes, which costs at most a
// few dozen tiny prompts a day against a daily or weekly quota.
var resetCheckDelays = []time.Duration{
	1 * time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute,
}

// resetCheckGrace keeps a probe-managed block in place past the probe's due
// time, long enough for the checker's tick plus one probe run. If the checker
// stalls (or the server restarts and forgets the probe), the block still
// expires on its own and the next task run finds out instead.
const resetCheckGrace = 5 * time.Minute

func resetCheckDelay(attempt int) time.Duration {
	if attempt >= len(resetCheckDelays) {
		return resetCheckDelays[len(resetCheckDelays)-1]
	}
	return resetCheckDelays[attempt]
}

// EnableResetChecks hands future unknown-reset limits to a ResetChecker.
func (r *RateLimitRegistry) EnableResetChecks() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resetChecks = true
}

// BlockUnknownReset blocks pool after a limit that carried no reset time.
// With reset checks enabled the pool stays blocked until a ResetChecker
// probe, run as cfg, gets through; otherwise it falls back to
// BlockWithBackoff. A pool already under a reset check keeps its schedule.
func (r *RateLimitRegistry) BlockUnknownReset(pool string, cfg AgentConfig) {
	r.mu.Lock()
	if !r.resetChecks {
		r.mu.Unlock()
		r.BlockWithBackoff(pool)
		return
	}
	defer r.mu.Unlock()
	now := time.Now()
	if st, ok := r.probes[pool]; ok {
		// A sibling run hit the same limit; the existing check covers it.
		r.blockedAt[pool] = now
		r.blocked[pool] = st.next.Add(resetCheckGrace)
		return
	}
	st := &resetCheckState{cfg: cfg, next: now.Add(resetCheckDelay(0))}
	r.probes[pool] = st
	until := st.next.Add(resetCheckGrace)
	r.blocked[pool] = until
	r.attempts[pool]++
	r.blockedAt[pool] = now
	r.persistSave(pool, until, r.attempts[pool])
}

// ResetCheck is one pool due for a probe.
type ResetCheck struct {
	Pool   string
	Config AgentConfig
}

// dueResetChecks returns the pools whose next probe is due. Probes for pools
// that are no longer blocked (cleared by a successful run, or expired) are
// dropped.
func (r *RateLimitRegistry) dueResetChecks(now time.Time) []ResetCheck {
	r.mu.Lock()
	defer r.mu.Unlock()
	var due []ResetCheck
	for pool, st := range r.probes {
		until, ok := r.blocked[pool]
		if !ok || !now.Before(until) {
			delete(r.probes, pool)
			continue
		}
		if !now.Before(st.next) {
			due = append(due, ResetCheck{Pool: pool, Config: st.cfg})
		}
	}
	return due
}

// resetCheckStillLimited records a probe that was still limited (or failed
// for another reason) and schedules the next one, extending the block to
// cover it.
func (r *RateLimitRegistry) resetCheckStillLimited(pool string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.probes[pool]
	if !ok {
		return
	}
	st.attempt++
	st.next = time.Now().Add(resetCheckDelay(st.attempt))
	until := st.next.Add(resetCheckGrace)
	r.blocked[pool] = until
	r.persistSave(pool, until, r.attempts[pool])
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
	delete(r.probes, agentConfigID)
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
	delete(r.probes, agentConfigID)
	r.persistDelete(agentConfigID)
	return true
}
