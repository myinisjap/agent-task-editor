package worktreesweep

import (
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// TestNew_WiresIntervalAndQueries verifies the trivial constructor stores
// what it's given.
func TestNew_WiresIntervalAndQueries(t *testing.T) {
	rt := &agent.RuntimeManager{}
	s := New(nil, 5*time.Minute, rt)
	if s == nil {
		t.Fatal("expected New to return a non-nil Sweeper")
	}
	if s.interval != 5*time.Minute {
		t.Errorf("expected interval 5m, got %v", s.interval)
	}
	// Guards the production-wiring bug this replaces: main.go used to build
	// the sweeper with no Runtime, leaving it nil and causing
	// reconcileContainers to treat every managed container as belonging to
	// a repo with no runtime source (see shouldReapContainer), reaping
	// healthy containers on the first tick. Runtime being a required
	// constructor param (rather than a field assigned separately) means the
	// compiler — not this test — is what actually prevents a future
	// regression at the main.go call site; this test only proves New itself
	// wires the argument through.
	if s.Runtime != rt {
		t.Error("expected New to wire the given RuntimeManager onto Sweeper.Runtime")
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
// stale, or the repo's runtime_image cleared back to empty — is reaped,
// UNLESS the repo has a task with an in-flight run, which always wins (see
// TestShouldReapContainer_SkipsReposWithActiveRun for that case).
func TestShouldReapContainer(t *testing.T) {
	states := map[string]repoRuntimeState{
		"repo-current": {Image: "ghcr.io/example/runtime:2"},
		"repo-cleared": {}, // runtime_image was unset after this container was created
	}
	noActiveRuns := map[string]struct{}{}

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
			if got := shouldReapContainer(tc.c, states, noActiveRuns); got != tc.want {
				t.Errorf("shouldReapContainer(%+v) = %v, want %v", tc.c, got, tc.want)
			}
		})
	}
}

// TestShouldReapContainer_SkipsReposWithActiveRun is the regression guard for
// the sweeper racing an in-flight run (EnsureRunning resolves the container
// name at startRun, well before the provider actually `docker exec`s into
// it — pool enqueue, prompt building, and MCP prep happen in between). A
// repo with an active run must never be reaped this tick, even when its
// image is stale or its repo is (implausibly) reported gone — removing the
// reposWithActiveRun check in shouldReapContainer would flip every case here
// to "reap" and fail this test.
func TestShouldReapContainer_SkipsReposWithActiveRun(t *testing.T) {
	states := map[string]repoRuntimeState{
		"repo-active": {Image: "ghcr.io/example/runtime:2"},
	}
	activeRuns := map[string]struct{}{"repo-active": {}}

	cases := []struct {
		name string
		c    agent.ManagedContainer
	}{
		{
			name: "image matches but repo has an active run: keep anyway",
			c:    agent.ManagedContainer{Name: "n1", RepoID: "repo-active", Image: "ghcr.io/example/runtime:2"},
		},
		{
			name: "image is stale but repo has an active run: keep anyway",
			c:    agent.ManagedContainer{Name: "n2", RepoID: "repo-active", Image: "ghcr.io/example/runtime:1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldReapContainer(tc.c, states, activeRuns); got != false {
				t.Errorf("shouldReapContainer(%+v) = %v, want false (in-flight run must block reaping)", tc.c, got)
			}
		})
	}
}
