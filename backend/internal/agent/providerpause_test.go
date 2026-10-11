package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderPauseRegistry_PauseResume(t *testing.T) {
	pub := &recordingPub{}
	r := NewProviderPauseRegistry(pub)

	if r.IsPaused("claude") {
		t.Fatal("new registry should not be paused")
	}
	if !r.Pause("claude", "not logged in") {
		t.Fatal("first Pause should report newly paused")
	}
	if r.Pause("claude", "again") {
		t.Fatal("second Pause should be a no-op")
	}
	if got := pub.count("provider.pause_changed"); got != 1 {
		t.Fatalf("events after duplicate pause = %d, want 1", got)
	}
	if l := r.List(); len(l) != 1 || l[0].Provider != "claude" || l[0].Reason != "not logged in" {
		t.Fatalf("List = %+v", l)
	}
	if !r.Resume("claude") || r.IsPaused("claude") {
		t.Fatal("Resume should clear the pause")
	}
	if r.Resume("claude") {
		t.Fatal("Resume of an unpaused provider should report false")
	}
	if got := pub.count("provider.pause_changed"); got != 2 {
		t.Fatalf("events = %d, want 2", got)
	}
}

func TestProviderPauseRegistry_NilSafe(t *testing.T) {
	var r *ProviderPauseRegistry
	if r.Pause("claude", "x") || r.Resume("claude") || r.IsPaused("claude") || len(r.List()) != 0 {
		t.Fatal("nil registry must be a no-op")
	}
}

func TestProviderPauseRegistry_RecheckResumesWhenProbePasses(t *testing.T) {
	r := NewProviderPauseRegistry(nil)
	r.Pause("claude", "x")
	var healthy atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.RunRecheck(ctx, 10*time.Millisecond, func(string) bool { return healthy.Load() })

	time.Sleep(60 * time.Millisecond)
	if !r.IsPaused("claude") {
		t.Fatal("must stay paused while the probe fails")
	}
	healthy.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for r.IsPaused("claude") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.IsPaused("claude") {
		t.Fatal("expected auto-resume once the probe passes")
	}
}
