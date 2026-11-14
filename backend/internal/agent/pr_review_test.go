package agent

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func prGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestOnTaskTerminal_ReviewSourceNeverPushes(t *testing.T) {
	ctx := context.Background()
	repo := initRepo(t)
	wt, branch, _, err := provisionWorktree(ctx, repo, "pr-task-0001", "x")
	if err != nil {
		t.Fatal(err)
	}
	var pushed bool
	withGitRunner(t, func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "push" {
			pushed = true
		}
		return exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	})
	store := &fakeTaskTerminalStore{}
	task := gen.Task{ID: "pr-task-0001", Source: "github_pr", Branch: branch, WorktreePath: wt}
	r := gen.Repo{Path: repo, RemoteUrl: strPtr("https://github.com/o/r.git")}
	if err := OnTaskTerminal(ctx, store, task, r); err != nil {
		t.Fatal(err)
	}
	if pushed {
		t.Fatal("review-only task must never push")
	}
	if store.calls != 1 {
		t.Fatal("worktree path should be cleared")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree should be removed")
	}
}

func TestSafetyNetCommit_SkipsReviewJobs(t *testing.T) {
	withGitRunner(t, func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		t.Fatalf("no git command may run for a review job, got %v", args)
		return nil, nil
	})
	p := &Pool{}
	for _, job := range []Job{
		{Input: RunInput{RepoPath: "/x", RepoRemoteURL: "u", Task: Task{Source: "gitea_pr", Branch: "b"}}},
		{Input: RunInput{RepoPath: "/x", RepoRemoteURL: "u", PRReview: &PRReviewInput{}, Task: Task{Branch: "b"}}},
	} {
		p.safetyNetCommit(context.Background(), job, "completed", slog.Default())
	}
}

func TestProvisionPRWorktree_ChecksOutPRHeadAndResets(t *testing.T) {
	ctx := context.Background()
	origin := initRepo(t)
	// A PR head: a commit published at refs/pull/7/head on the "remote".
	prGit(t, origin, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(origin, "new.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prGit(t, origin, "add", "-A")
	prGit(t, origin, "commit", "-m", "pr work")
	first := prGit(t, origin, "rev-parse", "HEAD")
	prGit(t, origin, "update-ref", "refs/pull/7/head", first)
	prGit(t, origin, "checkout", "main")

	clone := t.TempDir()
	prGit(t, clone, "clone", origin, ".")

	wt, branch, base, head, err := provisionPRWorktree(ctx, clone, "task-abcdef12-x", 7, "main")
	if err != nil {
		t.Fatal(err)
	}
	if branch != "pr-review-7-task-abc" || strings.HasPrefix(branch, "ate-") {
		t.Fatalf("branch %q", branch)
	}
	if head != first || base != "origin/main" {
		t.Fatalf("head=%s base=%s", head, base)
	}
	in := &PRReviewInput{BaseRef: "main"}
	fillPRDiff(ctx, in, wt, base)
	if !strings.Contains(in.Diff, "+one") {
		t.Fatalf("diff: %q", in.Diff)
	}

	// New push to the PR: a re-review resets the existing worktree.
	prGit(t, origin, "checkout", "feature")
	if err := os.WriteFile(filepath.Join(origin, "new.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prGit(t, origin, "commit", "-am", "more")
	second := prGit(t, origin, "rev-parse", "HEAD")
	prGit(t, origin, "update-ref", "refs/pull/7/head", second)
	_, _, _, head2, err := provisionPRWorktree(ctx, clone, "task-abcdef12-x", 7, "main")
	if err != nil || head2 != second {
		t.Fatalf("re-provision: head=%s err=%v", head2, err)
	}
	if _, _, _, _, err := provisionPRWorktree(ctx, clone, "../evil", 7, "main"); err == nil {
		t.Fatal("unsafe id must be rejected")
	}
	if _, _, _, _, err := provisionPRWorktree(ctx, clone, "task-1", 0, "main"); err == nil {
		t.Fatal("bad PR number must be rejected")
	}
}

func TestSanitizePRReview(t *testing.T) {
	notes := "from notes"
	got := sanitizePRReview(&PRReviewResult{
		Comments: []PRReviewComment{
			{Path: "a.go", Line: 3, Body: "ok"},
			{Path: "../etc/passwd", Line: 1, Body: "x"},
			{Path: "/abs", Line: 1, Body: "x"},
			{Path: "", Line: 1, Body: "x"},
			{Path: "b.go", Line: 0, Body: "x"},
		},
	}, Result{Notes: &notes})
	if len(got.Comments) != 1 || got.Comments[0].Path != "a.go" {
		t.Fatalf("comments: %+v", got.Comments)
	}
	if got.Summary != "from notes" {
		t.Fatalf("summary fallback: %q", got.Summary)
	}
	if s := sanitizePRReview(nil, Result{}).Summary; s != "No findings reported." {
		t.Fatalf("default summary %q", s)
	}
	many := &PRReviewResult{Summary: "s"}
	for i := 0; i < 80; i++ {
		many.Comments = append(many.Comments, PRReviewComment{Path: "a.go", Line: i + 1, Body: "b"})
	}
	if n := len(sanitizePRReview(many, Result{}).Comments); n != maxPRReviewComments {
		t.Fatalf("cap: %d", n)
	}
}
