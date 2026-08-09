package ghsync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// fakeSecondForge is a minimal forge.Forge implementation standing in for a
// second, non-GitHub forge (e.g. Gitea) — proving that New's getPR/
// getPRHead/getReviews/getReviewComments/getFailedChecks dispatch funcs call
// through to whichever forge.Forge resolveRepoInfo actually resolved for a
// repo (via repoInfo.forge), rather than being hardcoded to GitHub. This
// mirrors forge_test.go's fakeForge but lives here (rather than being
// imported) since forge_test.go's fakeForge is unexported in an external
// test package.
type fakeSecondForge struct {
	host  string
	calls []string
	// reviewComment, when true, makes PRReviewComments return one inline
	// comment (rather than none) — used by
	// TestIngestReviewComments_TagsSourceWithResolvedForgeName to exercise
	// the ingestion path that writes task_review_comments.source.
	reviewComment bool
}

func (f *fakeSecondForge) Name() string { return "fake-second-forge" }

func (f *fakeSecondForge) ParseRepoName(remoteURL string) (string, bool) {
	prefix := "https://" + f.host + "/"
	if len(remoteURL) > len(prefix) && remoteURL[:len(prefix)] == prefix {
		return remoteURL[len(prefix):], true
	}
	return "", false
}

func (f *fakeSecondForge) PRForBranch(ctx context.Context, repoName, branch string) (string, string, int, error) {
	f.calls = append(f.calls, "PRForBranch")
	return "pr_open", "https://" + f.host + "/" + repoName + "/pulls/1", 1, nil
}
func (f *fakeSecondForge) CreatePR(ctx context.Context, repoName, branch, base, title, body string) (string, string, error) {
	f.calls = append(f.calls, "CreatePR")
	return "", "", nil
}
func (f *fakeSecondForge) PRHead(ctx context.Context, repoName, branch string) (forge.PRHead, error) {
	f.calls = append(f.calls, "PRHead")
	return forge.PRHead{
		Number: 1, HeadSHA: "abc123", BaseRef: "main", Mergeable: forge.MergeableClean,
		State: "pr_open", URL: "https://" + f.host + "/" + repoName + "/pulls/1",
	}, nil
}
func (f *fakeSecondForge) PRReviews(ctx context.Context, repoName string, prNumber int) ([]forge.Review, error) {
	f.calls = append(f.calls, "PRReviews")
	return nil, nil
}
func (f *fakeSecondForge) PRReviewComments(ctx context.Context, repoName string, prNumber int) ([]forge.PRReviewComment, error) {
	f.calls = append(f.calls, "PRReviewComments")
	if !f.reviewComment {
		return nil, nil
	}
	return []forge.PRReviewComment{
		{ID: "c1", Path: "main.go", Line: 10, StartLine: 10, Side: "RIGHT", Body: "please fix", DiffHunk: "@@ -8,3 +8,3 @@", AuthorAssociation: "COLLABORATOR"},
	}, nil
}
func (f *fakeSecondForge) FailedChecks(ctx context.Context, repoName string, prNumber int) ([]forge.Check, error) {
	f.calls = append(f.calls, "FailedChecks")
	return nil, nil
}
func (f *fakeSecondForge) ListOpenIssues(ctx context.Context, repoName, label string) ([]forge.Issue, error) {
	return nil, nil
}
func (f *fakeSecondForge) GetIssueComments(ctx context.Context, repoName string, issueNumber int) ([]forge.IssueComment, error) {
	return nil, nil
}
func (f *fakeSecondForge) AddIssueLabel(ctx context.Context, repoName string, issueNumber int, label string) error {
	return nil
}
func (f *fakeSecondForge) CommentOnIssue(ctx context.Context, repoName string, issueNumber int, body string) error {
	return nil
}
func (f *fakeSecondForge) CloseIssueWithComment(ctx context.Context, repoName string, issueNumber int, body string) error {
	return nil
}
func (f *fakeSecondForge) AuthStatus() (bool, string) { return true, "fake" }
func (f *fakeSecondForge) CompareURL(repoName, base, branch, title, body string) string {
	return "https://" + f.host + "/" + repoName + "/compare/" + base + "..." + branch
}

var _ forge.Forge = (*fakeSecondForge)(nil)

// TestSyncer_DispatchesToResolvedForge_NotHardcodedToGitHub proves that a
// Syncer built via New() calls through to whichever forge.Forge
// forge.ForRemote resolves for a repo's remote URL — not always GitHub's
// ghclient implementation — closing the gap where getPRHead/getReviews/
// getReviewComments/getFailedChecks were previously bound once at
// construction time to ghclient.GitHub{} regardless of which forge actually
// owned a given repo.
func TestSyncer_DispatchesToResolvedForge_NotHardcodedToGitHub(t *testing.T) {
	fake := &fakeSecondForge{host: "git.example.test"}
	forge.Register(fake)

	f, err := os.CreateTemp("", "ghsync-multiforge-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	q := gen.New(db.SQL())
	hub := &fakeHub{}
	s := New(db.SQL(), hub, time.Hour, nil)
	s.hub = hub
	s.q = q

	repoPath := t.TempDir()
	wfID, label := newTestWorkflow(t, q)
	remote := "https://git.example.test/acme/widgets"
	repoID := newTestRepo(t, q, wfID, repoPath, &remote)
	branch := "feature-branch"
	wtPath := filepath.Join(t.TempDir(), "wt")
	task := newTestTask(t, q, repoID, wfID, label, branch, wtPath, "pushed", "")

	repo := s.resolveRepoInfo(context.Background(), repoID)
	if repo.ghName != "acme/widgets" {
		t.Fatalf("expected resolveRepoInfo to recognise the fake forge's remote, got ghName=%q", repo.ghName)
	}
	if repo.forge != fake {
		t.Fatalf("expected repoInfo.forge to be the registered fake forge, got %#v", repo.forge)
	}

	s.syncTask(context.Background(), task, repo)

	if len(fake.calls) == 0 {
		t.Fatal("expected the fake forge's methods to be called by syncTask, but none were")
	}
	found := false
	for _, c := range fake.calls {
		if c == "PRHead" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected PRHead to be called on the resolved fake forge, got calls: %v", fake.calls)
	}
	// #340: syncTask makes exactly one PR-lookup call per task per sweep now
	// (PRForBranch and PRHead used to be two near-identical calls).
	prForBranchCalls := 0
	for _, c := range fake.calls {
		if c == "PRForBranch" {
			prForBranchCalls++
		}
	}
	if prForBranchCalls != 0 {
		t.Errorf("expected PRForBranch NOT to be called by syncTask (folded into PRHead — #340), got %d calls", prForBranchCalls)
	}

	updated, err := q.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated.GitState != "pr_open" {
		t.Errorf("expected git_state updated via the fake forge's PRHead result, got %q", updated.GitState)
	}
}

// TestIngestReviewComments_TagsSourceWithResolvedForgeName proves that inline
// PR review comments ingested for a task on a non-GitHub forge (via
// repoInfo.forge, resolved per repo by forge.ForRemote) are tagged in
// task_review_comments.source with that forge's own Name() rather than being
// hardcoded to "github" — closing the gap where CreateGitHubTaskReviewComment
// always wrote source='github' regardless of which forge a PR review comment
// actually came from.
func TestIngestReviewComments_TagsSourceWithResolvedForgeName(t *testing.T) {
	fake := &fakeSecondForge{host: "git.example2.test", reviewComment: true}
	forge.Register(fake)

	f, err := os.CreateTemp("", "ghsync-multiforge-comments-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	q := gen.New(db.SQL())
	hub := &fakeHub{}
	s := New(db.SQL(), hub, time.Hour, nil)
	s.hub = hub
	s.q = q

	repoPath := t.TempDir()
	wfID, label := newTestWorkflow(t, q)
	remote := "https://git.example2.test/acme/widgets"
	repoID := newTestRepo(t, q, wfID, repoPath, &remote)
	branch := "feature-branch"
	wtPath := filepath.Join(t.TempDir(), "wt")
	task := newTestTask(t, q, repoID, wfID, label, branch, wtPath, "pr_open", "")

	repo := s.resolveRepoInfo(context.Background(), repoID)
	if repo.forge != fake {
		t.Fatalf("expected repoInfo.forge to be the registered fake forge, got %#v", repo.forge)
	}

	head, err := s.getPRHead(context.Background(), repo, branch)
	if err != nil {
		t.Fatalf("getPRHead: %v", err)
	}
	s.ingestPRFeedback(context.Background(), task, repo, head)

	comments, err := q.ListOpenTaskReviewComments(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 review comment, got %d", len(comments))
	}
	if comments[0].Source != fake.Name() {
		t.Errorf("comment source = %q, want %q (the resolved forge's Name())", comments[0].Source, fake.Name())
	}
}
