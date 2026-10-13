package agent

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// PausedProvider describes one provider kind whose dispatch is paused because
// a run was classified ClassAuth (the CLI is not logged in).
type PausedProvider struct {
	// Provider is the provider kind (e.g. "claude"), matching
	// provider_configs.provider.
	Provider string `json:"provider"`
	// Reason is the human-readable cause shown in the UI banner.
	Reason string `json:"reason"`
	// PausedAt is when the circuit tripped.
	PausedAt time.Time `json:"paused_at"`
}

// ProviderPauseRegistry is the provider circuit breaker. It is keyed by
// provider kind rather than by agent config because the thing that breaks is
// the CLI login, which every config of that kind shares (and the health
// checks are per kind too). State is in-memory only: after a restart the
// registry is empty, which is safe because the first run that hits the broken
// login re-trips it (costing at most one escalated task).
type ProviderPauseRegistry struct {
	mu     sync.Mutex
	paused map[string]PausedProvider
	pub    Publisher
}

// NewProviderPauseRegistry creates a registry. pub may be nil.
func NewProviderPauseRegistry(pub Publisher) *ProviderPauseRegistry {
	return &ProviderPauseRegistry{paused: make(map[string]PausedProvider), pub: pub}
}

// Pause trips the breaker for provider. It reports whether the provider was
// newly paused (false when it already was, so the original PausedAt is kept
// and no duplicate event is published). Safe on a nil registry.
func (r *ProviderPauseRegistry) Pause(provider, reason string) bool {
	if r == nil || provider == "" {
		return false
	}
	r.mu.Lock()
	if _, ok := r.paused[provider]; ok {
		r.mu.Unlock()
		return false
	}
	r.paused[provider] = PausedProvider{Provider: provider, Reason: reason, PausedAt: time.Now()}
	r.mu.Unlock()
	r.publish(provider, true)
	return true
}

// Resume clears the pause for provider and reports whether it was paused.
// Safe on a nil registry.
func (r *ProviderPauseRegistry) Resume(provider string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	_, ok := r.paused[provider]
	delete(r.paused, provider)
	r.mu.Unlock()
	if ok {
		r.publish(provider, false)
	}
	return ok
}

// IsPaused reports whether dispatch for provider is paused. Safe on a nil
// registry.
func (r *ProviderPauseRegistry) IsPaused(provider string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.paused[provider]
	return ok
}

// List returns the paused providers, ordered by pause time. Never nil.
func (r *ProviderPauseRegistry) List() []PausedProvider {
	out := []PausedProvider{}
	if r == nil {
		return out
	}
	r.mu.Lock()
	for _, p := range r.paused {
		out = append(out, p)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].PausedAt.Before(out[j].PausedAt) })
	return out
}

func (r *ProviderPauseRegistry) publish(provider string, paused bool) {
	if r.pub == nil {
		return
	}
	r.pub.Publish(wsevent.TypeProviderPauseChanged, wsevent.ProviderPauseChanged{
		Provider: provider,
		Paused:   paused,
	})
}

// RunRecheck periodically re-checks each paused provider with probe (the
// existing health check) and resumes the ones that now pass. A provider is
// only re-checked once it has been paused for at least interval, so a
// heuristic check that passes despite a bad login cannot flap the breaker
// faster than that. It blocks until ctx is cancelled.
func (r *ProviderPauseRegistry) RunRecheck(ctx context.Context, interval time.Duration, probe func(provider string) bool) {
	if r == nil || probe == nil || interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, p := range r.List() {
				if time.Since(p.PausedAt) < interval {
					continue
				}
				if probe(p.Provider) {
					r.Resume(p.Provider)
				}
			}
		}
	}
}
