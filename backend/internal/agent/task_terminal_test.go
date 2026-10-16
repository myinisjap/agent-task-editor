package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// fakeTaskTerminalStore is a minimal TaskTerminalStore fake recording whether
// ClearTaskWorktreePath was called, and for which task id.
type fakeTaskTerminalStore struct {
	clearedID string
	clearErr  error
	calls     int
}

func (f *fakeTaskTerminalStore) ClearTaskWorktreePath(ctx context.Context, id string) error {
	f.calls++
	f.clearedID = id
	return f.clearErr
}

func strPtr(s string) *string { return &s }

// TestOnTaskTerminal_MergedTaskMissingWorktree_NoErrorNoPush verifies that
// once a task's PR is confirmed merged (git_state "pr_merged") and its
// worktree directory no longer exists — the common case once ghsync's
// cleanupMergedBranch has already torn it down — OnTaskTerminal completes
// without error, never attempts a push, and clears the stale worktree_path.
// This is the regression covered by the "Branch push failed" bug: previously
// the inline OnTerminal hook in cmd/server unconditionally tried to push from
// task.WorktreePath and failed with "No such file or directory".
func TestOnTaskTerminal_MergedTaskMissingWorktree_NoErrorNoPush(t *testing.T) {
	ctx := context.Background()
	repo := initRepo(t)

	pushed := false
	withGitRunner(t, func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "push" {
			pushed = true
		}
		if len(args) > 0 && args[0] == "worktree" {
			// No real worktree exists on disk for this test (we never
			// provisioned one), so there is nothing for `git worktree
			// remove`/`prune` to act on; just report success either way.
			return []byte("ok"), nil
		}
		return []byte("ok"), nil
	})

	store := &fakeTaskTerminalStore{}
	task := gen.Task{
		ID:           "task-merged-1",
		RepoID:       "repo-1",
		Branch:       "ate-merged-task1",
		WorktreePath: "/nonexistent/path/that/was/already/removed",
		GitState:     "pr_merged",
	}
	r := gen.Repo{ID: "repo-1", Path: repo, RemoteUrl: strPtr("https://example.com/org/repo.git")}

	if err := OnTaskTerminal(ctx, store, task, r); err != nil {
		t.Fatalf("OnTaskTerminal: unexpected error for merged task with missing worktree: %v", err)
	}
	if pushed {
		t.Fatalf("expected no push for an already-merged task, but git push was invoked")
	}
	if store.calls != 1 || store.clearedID != task.ID {
		t.Fatalf("expected ClearTaskWorktreePath(%q) to be called once, got calls=%d id=%q", task.ID, store.calls, store.clearedID)
	}
}

// TestOnTaskTerminal_NormalTask_PushesAndRemovesWorktree verifies the normal
// (non-merged) path still pushes the branch to origin and tears down the
// worktree, keeping the existing pre-fix behavior for ordinary tasks.
func TestOnTaskTerminal_NormalTask_PushesAndRemovesWorktree(t *testing.T) {
	ctx := context.Background()
	repo := initRepo(t)

	wt, branch, _, err := provisionWorktree(ctx, repo, "task-normal1", "Some change")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	var pushArgs []string
	withGitRunner(t, func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "push" {
			pushArgs = args
			return []byte("ok"), nil
		}
		// Worktree removal and anything else goes through the real git binary
		// so the on-disk worktree is actually torn down (verified below).
		return runGit(ctx, dir, args...)
	})

	store := &fakeTaskTerminalStore{}
	task := gen.Task{
		ID:           "task-normal-1",
		RepoID:       "repo-1",
		Branch:       branch,
		WorktreePath: wt,
		GitState:     "review",
	}
	r := gen.Repo{ID: "repo-1", Path: repo, RemoteUrl: strPtr("https://example.com/org/repo.git")}

	if err := OnTaskTerminal(ctx, store, task, r); err != nil {
		t.Fatalf("OnTaskTerminal: unexpected error: %v", err)
	}

	if len(pushArgs) == 0 {
		t.Fatalf("expected a git push for a non-merged task, got none")
	}
	want := []string{"push", "-u", "origin", branch}
	if strings.Join(pushArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("push args = %v, want %v", pushArgs, want)
	}

	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("expected worktree dir %q to be removed", wt)
	}
	if store.calls != 1 || store.clearedID != task.ID {
		t.Fatalf("expected ClearTaskWorktreePath(%q) to be called once, got calls=%d id=%q", task.ID, store.calls, store.clearedID)
	}
}

// TestOnTaskTerminal_NoWorktreePath_NoOp verifies a task that never had (or
// already lost) its worktree_path is a pure no-op: no push, no store call.
func TestOnTaskTerminal_NoWorktreePath_NoOp(t *testing.T) {
	ctx := context.Background()
	pushed := false
	withGitRunner(t, func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		pushed = true
		return []byte("ok"), nil
	})

	store := &fakeTaskTerminalStore{}
	task := gen.Task{ID: "task-none", RepoID: "repo-1", Branch: "whatever", WorktreePath: "", GitState: "done"}
	r := gen.Repo{ID: "repo-1", Path: "/tmp/does-not-matter", RemoteUrl: strPtr("https://example.com/org/repo.git")}

	if err := OnTaskTerminal(ctx, store, task, r); err != nil {
		t.Fatalf("OnTaskTerminal: unexpected error: %v", err)
	}
	if pushed {
		t.Fatalf("expected no git invocation when WorktreePath is empty")
	}
	if store.calls != 0 {
		t.Fatalf("expected ClearTaskWorktreePath not to be called, got %d calls", store.calls)
	}
}

// TestOnTaskTerminal_PushFailure_PropagatesError verifies a failing push is
// surfaced as an error (so the engine's side-effect outbox retries it) and
// that the worktree is left alone / worktree_path is not cleared on failure.
func TestOnTaskTerminal_PushFailure_PropagatesError(t *testing.T) {
	ctx := context.Background()
	repo := initRepo(t)

	withGitRunner(t, func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "push" {
			return []byte("! [rejected]"), errFakeGit
		}
		return []byte("ok"), nil
	})

	store := &fakeTaskTerminalStore{}
	task := gen.Task{
		ID:           "task-pushfail-1",
		RepoID:       "repo-1",
		Branch:       "ate-pushfail",
		WorktreePath: "/some/worktree/path",
		GitState:     "review",
	}
	r := gen.Repo{ID: "repo-1", Path: repo, RemoteUrl: strPtr("https://example.com/org/repo.git")}

	err := OnTaskTerminal(ctx, store, task, r)
	if err == nil {
		t.Fatal("expected an error from a failing push")
	}
	if !strings.Contains(err.Error(), "push branch") {
		t.Errorf("expected error to mention the push, got: %v", err)
	}
	if store.calls != 0 {
		t.Errorf("worktree_path should not be cleared when the push fails, got %d clear calls", store.calls)
	}
}

// TestOnTaskTerminal_ClearWorktreePathFailure_PropagatesError verifies a
// failure to clear worktree_path is surfaced as an error even though the
// git-level work (push + remove) already succeeded, so a retry can at least
// pick the clear back up (it is itself idempotent).
func TestOnTaskTerminal_ClearWorktreePathFailure_PropagatesError(t *testing.T) {
	ctx := context.Background()
	repo := initRepo(t)

	wt, branch, _, err := provisionWorktree(ctx, repo, "task-clearfail1", "Some change")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	withGitRunner(t, func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "push" {
			return []byte("ok"), nil
		}
		return runGit(ctx, dir, args...)
	})

	wantErr := errors.New("db write failed")
	store := &fakeTaskTerminalStore{clearErr: wantErr}
	task := gen.Task{
		ID:           "task-clearfail-1",
		RepoID:       "repo-1",
		Branch:       branch,
		WorktreePath: wt,
		GitState:     "review",
	}
	r := gen.Repo{ID: "repo-1", Path: repo, RemoteUrl: strPtr("https://example.com/org/repo.git")}

	gotErr := OnTaskTerminal(ctx, store, task, r)
	if gotErr == nil {
		t.Fatal("expected an error when ClearTaskWorktreePath fails")
	}
	if !errors.Is(gotErr, wantErr) {
		t.Errorf("expected wrapped store error, got: %v", gotErr)
	}
	if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
		t.Errorf("expected worktree dir to still be removed despite the later clear failure")
	}
}
