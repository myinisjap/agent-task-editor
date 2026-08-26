package worktreesweep

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
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

// TestResolveRepoRuntimeState_ResolutionPrecedence covers
// resolveRepoRuntimeState's source precedence, mirroring dispatcher.go's
// startRun resolution order: an explicit RuntimeImage always wins; otherwise
// a repo-committed .devcontainer/devcontainer.json beats runtime_languages;
// with neither, the repo gets the zero state (any container found for it is
// always reaped).
func TestResolveRepoRuntimeState_ResolutionPrecedence(t *testing.T) {
	rt := &agent.RuntimeManager{}

	repoImage := repoAt(t, "")
	repoImage.RuntimeImage = "ghcr.io/example/runtime:1"

	repoFile := repoAt(t, "")
	if err := os.MkdirAll(filepath.Join(repoFile.Path, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoFile.Path, ".devcontainer", "devcontainer.json"), []byte(`{"image":"golang:1.26"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	repoLangs := repoAt(t, "")
	repoLangs.RuntimeLanguages = `[{"id":"go","version":"1.26"}]`

	repoImageAndFile := repoAt(t, "")
	repoImageAndFile.RuntimeImage = "ghcr.io/example/runtime:2"
	if err := os.MkdirAll(filepath.Join(repoImageAndFile.Path, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoImageAndFile.Path, ".devcontainer", "devcontainer.json"), []byte(`{"image":"golang:1.26"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	repoFileAndLangs := repoAt(t, "")
	repoFileAndLangs.RuntimeLanguages = `[{"id":"go","version":"1.26"}]`
	if err := os.MkdirAll(filepath.Join(repoFileAndLangs.Path, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoFileAndLangs.Path, ".devcontainer", "devcontainer.json"), []byte(`{"image":"golang:1.26"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	repoNone := repoAt(t, "")

	s := &Sweeper{Runtime: rt}
	states := s.resolveRepoRuntimeState([]gen.Repo{
		repoImage, repoFile, repoLangs, repoImageAndFile, repoFileAndLangs, repoNone,
	})

	if states[repoImage.ID].Image != repoImage.RuntimeImage {
		t.Errorf("expected explicit RuntimeImage to resolve as Image, got %+v", states[repoImage.ID])
	}
	if states[repoFile.ID].DevcontainerHash == "" {
		t.Errorf("expected a repo-committed devcontainer.json to resolve a DevcontainerHash, got %+v", states[repoFile.ID])
	}
	if states[repoLangs.ID].DevcontainerHash == "" {
		t.Errorf("expected runtime_languages to resolve a DevcontainerHash, got %+v", states[repoLangs.ID])
	}
	if states[repoImageAndFile.ID].Image != repoImageAndFile.RuntimeImage || states[repoImageAndFile.ID].DevcontainerHash != "" {
		t.Errorf("expected RuntimeImage to win over a committed devcontainer.json, got %+v", states[repoImageAndFile.ID])
	}
	// Step 2 (repo file) must win over step 3 (runtime_languages): the hash
	// should match ExpectedDevcontainerHashFromFile's raw-file hash, not
	// ExpectedDevcontainerHash's generated-from-langs hash.
	wantFileWinsHash := rt.ExpectedDevcontainerHashFromFile(`{"image":"golang:1.26"}`)
	if states[repoFileAndLangs.ID].DevcontainerHash != wantFileWinsHash {
		t.Errorf("expected repo-committed devcontainer.json to win over runtime_languages, got hash %q, want %q", states[repoFileAndLangs.ID].DevcontainerHash, wantFileWinsHash)
	}
	if got := states[repoNone.ID]; got != (repoRuntimeState{}) {
		t.Errorf("expected zero state for a repo with no runtime source, got %+v", got)
	}
}

// repoAt returns a minimal gen.Repo with a fresh temp dir as its Path (a
// distinct id per call), for resolveRepoRuntimeState tests that need
// ReadRepoDevcontainerFile to read real files from disk.
func repoAt(t *testing.T, id string) gen.Repo {
	t.Helper()
	if id == "" {
		id = t.TempDir() // reuse TempDir's uniqueness as a cheap unique id too
	}
	return gen.Repo{ID: id, Path: t.TempDir()}
}
