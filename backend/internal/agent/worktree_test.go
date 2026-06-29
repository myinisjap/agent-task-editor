package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "init")
	return dir
}

func TestBranchNameIsSlugOnly(t *testing.T) {
	got := branchName("3f9a1c2b-dead-beef", "Fix: Login/Redirect Bug!!")
	if strings.ContainsAny(got, " /:_!") {
		t.Fatalf("branch name has illegal chars: %q", got)
	}
	if !strings.HasPrefix(got, "ate-fix-log") {
		t.Fatalf("unexpected branch name: %q", got)
	}
	if !strings.HasSuffix(got, "-3f9a1c2b") {
		t.Fatalf("short id missing: %q", got)
	}
	if len(got) > 20 {
		t.Fatalf("branch name too long (%d > 20): %q", len(got), got)
	}
}

func TestProvisionIsIdempotentAndDiffsTaskWork(t *testing.T) {
	ctx := context.Background()
	repo := initRepo(t)

	wt1, branch, base, err := provisionWorktree(ctx, repo, "task-1234abcd", "Add a feature")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	// Second call returns the same worktree, doesn't error on existing dir.
	wt2, _, _, err := provisionWorktree(ctx, repo, "task-1234abcd", "Add a feature")
	if err != nil {
		t.Fatalf("re-provision: %v", err)
	}
	if wt1 != wt2 {
		t.Fatalf("worktree path not stable: %q vs %q", wt1, wt2)
	}

	// Agent leaves an uncommitted change; safety-net commits it.
	if err := os.WriteFile(filepath.Join(wt1, "new.txt"), []byte("work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := commitIfDirty(ctx, wt1, "task work"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// Clean tree → no-op, no error.
	if err := commitIfDirty(ctx, wt1, "noop"); err != nil {
		t.Fatalf("commit on clean tree: %v", err)
	}

	diff, err := diffAgainstBase(ctx, wt1, base, branch)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff, "new.txt") || !strings.Contains(diff, "+work") {
		t.Fatalf("diff missing task work:\n%s", diff)
	}

	// After teardown the worktree is gone, but the branch is kept in the main
	// clone — diffing from the repo dir must still show the task's work. This is
	// what the /tasks/{id}/diff handler falls back to for terminal tasks.
	if err := RemoveWorktree(ctx, repo, wt1); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}
	if _, err := os.Stat(wt1); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone")
	}
	diff2, err := diffAgainstBase(ctx, repo, base, branch)
	if err != nil {
		t.Fatalf("diff from main clone after teardown: %v", err)
	}
	if !strings.Contains(diff2, "new.txt") || !strings.Contains(diff2, "+work") {
		t.Fatalf("post-teardown diff missing task work:\n%s", diff2)
	}
}
