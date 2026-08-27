package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireBinary skips the test if name isn't on PATH — these tests exercise
// real git/uv/python subprocess behavior (the whole point of finding 3/4's
// regression guards), so they're skipped rather than faked in an environment
// missing one of them.
func requireBinary(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not on PATH: %v", name, err)
	}
	return path
}

// initGitRepo creates a fresh git repo with one commit, for tests that need
// a real linked worktree (info/exclude only exists/matters relative to a
// real .git dir).
func initGitRepo(t *testing.T) string {
	t.Helper()
	requireBinary(t, "git")
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

// addWorktree adds a linked worktree at <repoDir>/wt on a fresh branch,
// mirroring how the dispatcher provisions task worktrees (see
// agent.provisionWorktree) — a *linked* worktree, not the main checkout,
// since finding 3 is specifically about linked-worktree exclude behavior.
func addWorktree(t *testing.T, repoDir string) string {
	t.Helper()
	wtDir := filepath.Join(repoDir, "wt")
	cmd := exec.Command("git", "-C", repoDir, "worktree", "add", "-q", "-b", "feat", wtDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	return wtDir
}

func gitPorcelainStatus(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status --porcelain: %v: %s", err, out)
	}
	return string(out)
}

// TestEnsureVenv_NotVisibleInGitStatus is finding 3's regression guard: a
// populated .venv created inside a linked worktree must never show up in
// `git status --porcelain` for that worktree — otherwise worktree.go's
// safety-net `git add -A` commit (commitIfDirty) would sweep it into the
// task's branch for any repo that doesn't already gitignore .venv.
func TestEnsureVenv_NotVisibleInGitStatus(t *testing.T) {
	requireBinary(t, "uv")
	pythonPath := requireBinary(t, "python3")

	repoDir := initGitRepo(t)
	wtDir := addWorktree(t, repoDir)

	if err := EnsureVenv(context.Background(), wtDir, pythonPath, "3"); err != nil {
		t.Fatalf("EnsureVenv: %v", err)
	}

	// Sanity: the venv actually exists and is populated (a real dir with
	// real files), not just an empty stat target.
	entries, err := os.ReadDir(filepath.Join(wtDir, ".venv"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a populated .venv dir, err=%v entries=%v", err, entries)
	}

	status := gitPorcelainStatus(t, wtDir)
	if strings.Contains(status, ".venv") {
		t.Errorf("expected .venv to be excluded from git status, got:\n%s", status)
	}
}

// TestEnsureVenv_ReuseOnMatch verifies a second EnsureVenv call for the same
// pin version does not touch (recreate) an existing, matching venv.
func TestEnsureVenv_ReuseOnMatch(t *testing.T) {
	requireBinary(t, "uv")
	pythonPath := requireBinary(t, "python3")

	repoDir := initGitRepo(t)
	wtDir := addWorktree(t, repoDir)

	if err := EnsureVenv(context.Background(), wtDir, pythonPath, "3"); err != nil {
		t.Fatalf("EnsureVenv (create): %v", err)
	}

	marker := filepath.Join(wtDir, ".venv", "marker")
	if err := os.WriteFile(marker, []byte("sentinel"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureVenv(context.Background(), wtDir, pythonPath, "3"); err != nil {
		t.Fatalf("EnsureVenv (reuse): %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("expected existing venv to be reused (marker file preserved), got: %v", err)
	}
}

// TestEnsureVenv_RecreateOnMismatch verifies a pin-version change forces the
// stale venv to be removed and recreated, rather than silently reused —
// finding 4's fail-closed regression guard.
func TestEnsureVenv_RecreateOnMismatch(t *testing.T) {
	requireBinary(t, "uv")
	pythonPath := requireBinary(t, "python3")

	repoDir := initGitRepo(t)
	wtDir := addWorktree(t, repoDir)

	if err := EnsureVenv(context.Background(), wtDir, pythonPath, "3"); err != nil {
		t.Fatalf("EnsureVenv (create): %v", err)
	}

	marker := filepath.Join(wtDir, ".venv", "marker")
	if err := os.WriteFile(marker, []byte("sentinel"), 0644); err != nil {
		t.Fatal(err)
	}

	// A pin version that can never match the real interpreter's resolved
	// version forces a recreate.
	if err := EnsureVenv(context.Background(), wtDir, pythonPath, "99.99.99"); err != nil {
		t.Fatalf("EnsureVenv (recreate): %v", err)
	}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("expected stale venv to be recreated (marker file gone), got err=%v", err)
	}
	entries, err := os.ReadDir(filepath.Join(wtDir, ".venv"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a freshly-populated .venv after recreate, err=%v entries=%v", err, entries)
	}
}

func TestVenvMatchesVersion(t *testing.T) {
	dir := t.TempDir()
	cfg := "home = /usr\nversion = 3.11.7\n"
	if err := os.WriteFile(filepath.Join(dir, "pyvenv.cfg"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		pin  string
		want bool
	}{
		{"3.11.7", true}, // exact match
		{"3.11", true},   // pin is a version-family prefix
		{"3.12", false},  // different minor
		{"3", true},      // pin is a coarser version-family prefix ("3.11.7" starts with "3.")
		{"9.9.9", false},
	}
	for _, tt := range tests {
		if got := venvMatchesVersion(dir, tt.pin); got != tt.want {
			t.Errorf("venvMatchesVersion(%q) = %v, want %v", tt.pin, got, tt.want)
		}
	}
}

func TestVenvMatchesVersion_MissingConfig(t *testing.T) {
	if venvMatchesVersion(t.TempDir(), "3.11") {
		t.Error("expected false for a venv dir with no pyvenv.cfg")
	}
}
