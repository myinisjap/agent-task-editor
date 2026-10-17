package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// probeProvider returns a fixed error from Run and records what it was run with.
type probeProvider struct {
	err   error
	input RunInput
	calls int
}

func (p *probeProvider) Run(_ context.Context, in RunInput, logCh chan<- LogEntry) (Result, error) {
	p.calls++
	p.input = in
	logCh <- LogEntry{Type: LogStdout, Content: "OK"}
	return Result{Status: "completed"}, p.err
}

// dueNow makes pool's pending reset check due immediately.
func dueNow(r *RateLimitRegistry, pool string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes[pool].next = time.Now().Add(-time.Second)
}

func TestBlockUnknownReset_WithoutChecker_UsesBackoff(t *testing.T) {
	r := NewRateLimitRegistry()
	r.BlockUnknownReset("pool", AgentConfig{ID: "a"})
	blocked, until := r.IsBlocked("pool")
	if !blocked {
		t.Fatal("expected pool blocked")
	}
	if d := time.Until(until); d > BackoffDuration(0)+time.Second {
		t.Errorf("expected the backoff ladder's first step, got %v", d)
	}
	if len(r.probes) != 0 {
		t.Error("expected no reset check without a checker")
	}
}

func TestBlockUnknownReset_SchedulesResetCheck(t *testing.T) {
	r := NewRateLimitRegistry()
	r.EnableResetChecks()
	r.BlockUnknownReset("pool", AgentConfig{ID: "a", Provider: "opencode"})

	blocked, until := r.IsBlocked("pool")
	if !blocked {
		t.Fatal("expected pool blocked")
	}
	want := resetCheckDelay(0) + resetCheckGrace
	if d := time.Until(until); d < want-time.Second || d > want+time.Second {
		t.Errorf("expected block for first probe delay + grace (%v), got %v", want, d)
	}
	if due := r.dueResetChecks(time.Now()); len(due) != 0 {
		t.Errorf("expected no probe due yet, got %v", due)
	}
	due := r.dueResetChecks(time.Now().Add(resetCheckDelay(0) + time.Second))
	if len(due) != 1 || due[0].Pool != "pool" || due[0].Config.ID != "a" {
		t.Fatalf("expected one due check for pool as config a, got %+v", due)
	}

	// A known reset time replaces the check.
	r.Block("pool", time.Now().Add(time.Hour))
	if len(r.probes) != 0 {
		t.Error("expected Block with a reset time to drop the reset check")
	}
}

func TestResetCheckStillLimited_StepsDelayAndExtendsBlock(t *testing.T) {
	r := NewRateLimitRegistry()
	r.EnableResetChecks()
	r.BlockUnknownReset("pool", AgentConfig{ID: "a"})
	for i := 1; i <= len(resetCheckDelays)+2; i++ {
		r.resetCheckStillLimited("pool")
		_, until := r.IsBlocked("pool")
		want := resetCheckDelay(i) + resetCheckGrace
		if d := time.Until(until); d < want-time.Second || d > want+time.Second {
			t.Fatalf("after %d failed probes expected block ~%v, got %v", i, want, d)
		}
	}
	if resetCheckDelay(100) != 15*time.Minute {
		t.Errorf("expected delays to settle at 15m, got %v", resetCheckDelay(100))
	}
}

func TestResetChecker_Outcomes(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantBlocked bool
		wantProbe   bool // a further reset check still scheduled
	}{
		{"clean answer unblocks", nil, false, false},
		{"one-turn cap still means it answered", &ErrMaxTurns{MaxTurns: 1}, false, false},
		{"still limited reschedules", &ErrRateLimit{Message: "429"}, true, true},
		{"transient keeps it blocked", &ErrTransient{Cause: errors.New("connection reset")}, true, true},
		{"known reset time switches to a plain block", &ErrRateLimit{ResetAt: time.Now().Add(3 * time.Hour)}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRateLimitRegistry()
			prov := &probeProvider{err: c.err}
			chk := &ResetChecker{Limits: r, NewProvider: func(AgentConfig) Provider { return prov }}
			r.EnableResetChecks()
			r.BlockUnknownReset("pool", AgentConfig{ID: "a", Provider: "qwen_code", MaxTurns: 50, SystemPrompt: "long"})
			dueNow(r, "pool")

			chk.checkDue(context.Background())

			if prov.calls != 1 {
				t.Fatalf("expected one probe, got %d", prov.calls)
			}
			if got := prov.input.AgentConfig; got.MaxTurns != 1 || got.SystemPrompt != "" {
				t.Errorf("expected a minimal probe config, got MaxTurns=%d SystemPrompt=%q", got.MaxTurns, got.SystemPrompt)
			}
			if blocked, _ := r.IsBlocked("pool"); blocked != c.wantBlocked {
				t.Errorf("blocked = %v, want %v", blocked, c.wantBlocked)
			}
			if _, ok := r.probes["pool"]; ok != c.wantProbe {
				t.Errorf("reset check scheduled = %v, want %v", ok, c.wantProbe)
			}
		})
	}
}

// TestResetChecker_DropsCheckOnceUnblocked: a pool cleared some other way
// (a successful run, or a human) is not probed.
func TestResetChecker_DropsCheckOnceUnblocked(t *testing.T) {
	r := NewRateLimitRegistry()
	prov := &probeProvider{}
	chk := &ResetChecker{Limits: r, NewProvider: func(AgentConfig) Provider { return prov }}
	r.EnableResetChecks()
	r.BlockUnknownReset("pool", AgentConfig{ID: "a"})
	r.Unblock("pool")

	chk.checkDue(context.Background())
	if prov.calls != 0 {
		t.Errorf("expected no probe for an unblocked pool, got %d", prov.calls)
	}
}
