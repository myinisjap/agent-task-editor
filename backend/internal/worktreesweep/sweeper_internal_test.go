package worktreesweep

import (
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// TestNew_WiresIntervalAndQueries verifies the trivial constructor stores
// what it's given.
func TestNew_WiresIntervalAndQueries(t *testing.T) {
	s := New(nil, 5*time.Minute)
	if s == nil {
		t.Fatal("expected New to return a non-nil Sweeper")
	}
	if s.interval != 5*time.Minute {
		t.Errorf("expected interval 5m, got %v", s.interval)
	}
}

// TestCurrentInterval covers currentInterval's floor-at-MinInterval branch
// (a configured interval below the 1-minute floor is clamped up) and the
// pass-through branch (anything at/above the floor is used as-is).
func TestCurrentInterval(t *testing.T) {
	cases := []struct {
		name     string
		interval time.Duration
		want     time.Duration
	}{
		{"below floor is clamped up", 10 * time.Second, MinInterval},
		{"zero is clamped up", 0, MinInterval},
		{"exactly at floor is unchanged", MinInterval, MinInterval},
		{"above floor is unchanged", 10 * time.Minute, 10 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Sweeper{interval: tc.interval}
			if got := s.currentInterval(); got != tc.want {
				t.Errorf("currentInterval() with configured %v = %v, want %v", tc.interval, got, tc.want)
			}
		})
	}
}

// TestShouldReapContainer covers the keep-vs-reap decision for runtime
// containers (see internal/agent/runtime.go): a container survives only
// while its repo still exists AND its ate.image label matches that repo's
// current (non-empty) runtime_image. Anything else — repo deleted, image
// changed, or runtime_image cleared back to "" — is reaped.
func TestShouldReapContainer(t *testing.T) {
	imageByRepoID := map[string]string{
		"repo-current": "ghcr.io/example/runtime:2",
		"repo-cleared": "", // runtime_image was unset after this container was created
	}

	cases := []struct {
		name string
		c    agent.ManagedContainer
		want bool
	}{
		{
			name: "repo exists and image matches: keep",
			c:    agent.ManagedContainer{Name: "n1", RepoID: "repo-current", Image: "ghcr.io/example/runtime:2"},
			want: false,
		},
		{
			name: "repo exists but image is stale: reap",
			c:    agent.ManagedContainer{Name: "n2", RepoID: "repo-current", Image: "ghcr.io/example/runtime:1"},
			want: true,
		},
		{
			name: "repo no longer exists: reap",
			c:    agent.ManagedContainer{Name: "n3", RepoID: "repo-deleted", Image: "ghcr.io/example/runtime:2"},
			want: true,
		},
		{
			name: "repo exists but runtime_image was cleared: reap",
			c:    agent.ManagedContainer{Name: "n4", RepoID: "repo-cleared", Image: "ghcr.io/example/runtime:2"},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldReapContainer(tc.c, imageByRepoID); got != tc.want {
				t.Errorf("shouldReapContainer(%+v) = %v, want %v", tc.c, got, tc.want)
			}
		})
	}
}
