package writeback

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// fakeOtherForge is a minimal forge.Forge that recognises a single test-only
// host, letting these tests exercise resolveForgeForTask's per-repo
// forge.ForRemote routing (used for any non-"github" task source, e.g.
// "gitea") without depending on a real Gitea/GitHub implementation package
// (which would risk an import cycle from this package's own test binary back
// into ghclient, which already imports writeback... actually it doesn't, but
// keeping this self-contained avoids ever needing to care).
type fakeOtherForge struct {
	labelCalls   []string
	commentCalls []string
	closeCalls   []string
}

func (f *fakeOtherForge) Name() string { return "fake-other-forge" }

func (f *fakeOtherForge) ParseRepoName(remoteURL string) (string, bool) {
	const prefix = "https://forge.test/"
	if len(remoteURL) > len(prefix) && remoteURL[:len(prefix)] == prefix {
		return remoteURL[len(prefix):], true
	}
	return "", false
}
func (f *fakeOtherForge) PRForBranch(ctx context.Context, repoName, branch string) (string, string, int, error) {
	return "", "", 0, nil
}
func (f *fakeOtherForge) CreatePR(ctx context.Context, repoName, branch, base, title, body string) (string, string, error) {
	return "", "", nil
}
func (f *fakeOtherForge) PRHead(ctx context.Context, repoName, branch string) (forge.PRHead, error) {
	return forge.PRHead{}, nil
}
func (f *fakeOtherForge) PRReviews(ctx context.Context, repoName string, prNumber int) ([]forge.Review, error) {
	return nil, nil
}
func (f *fakeOtherForge) PRReviewComments(ctx context.Context, repoName string, prNumber int) ([]forge.PRReviewComment, error) {
	return nil, nil
}
func (f *fakeOtherForge) FailedChecks(ctx context.Context, repoName string, prNumber int) ([]forge.Check, error) {
	return nil, nil
}
func (f *fakeOtherForge) ListOpenIssues(ctx context.Context, repoName, label string) ([]forge.Issue, error) {
	return nil, nil
}
func (f *fakeOtherForge) GetIssueComments(ctx context.Context, repoName string, issueNumber int) ([]forge.IssueComment, error) {
	return nil, nil
}
func (f *fakeOtherForge) AddIssueLabel(ctx context.Context, repoName string, issueNumber int, label string) error {
	f.labelCalls = append(f.labelCalls, label)
	return nil
}
func (f *fakeOtherForge) CommentOnIssue(ctx context.Context, repoName string, issueNumber int, body string) error {
	f.commentCalls = append(f.commentCalls, body)
	return nil
}
func (f *fakeOtherForge) CloseIssueWithComment(ctx context.Context, repoName string, issueNumber int, body string) error {
	f.closeCalls = append(f.closeCalls, body)
	return nil
}
func (f *fakeOtherForge) AuthStatus() (bool, string) { return true, "fake" }
func (f *fakeOtherForge) CompareURL(repoName, base, branch, title, body string) string {
	return "https://forge.test/" + repoName
}

var _ forge.Forge = (*fakeOtherForge)(nil)

// seedRepoWithRemote is like seedRepo but with a caller-chosen remote URL, so
// tests can point a repo at a non-GitHub forge.
func seedRepoWithRemote(t *testing.T, q *gen.Queries, writebackEnabled bool, remoteURL string) gen.Repo {
	t.Helper()
	ctx := context.Background()

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{
		ID:   uuid.NewString(),
		Name: "wf-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	wbEnabled := int64(0)
	if writebackEnabled {
		wbEnabled = 1
	}
	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID:                    uuid.NewString(),
		Name:                  "acme/widgets",
		Path:                  t.TempDir(),
		RemoteUrl:             &remoteURL,
		WorkflowID:            &wf.ID,
		IssueSyncEnabled:      0,
		IssueSyncLabel:        "",
		IssueWritebackEnabled: wbEnabled,
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return repo
}

// seedSourcedTaskFrom is like seedSourcedTask but with a caller-chosen
// tasks.source value, so tests can simulate a task imported from a
// non-GitHub source (e.g. "gitea").
func seedSourcedTaskFrom(t *testing.T, q *gen.Queries, repo gen.Repo, source, sourceRef string) gen.Task {
	t.Helper()
	ctx := context.Background()
	task, err := q.CreateSourcedTask(ctx, gen.CreateSourcedTaskParams{
		ID:          uuid.NewString(),
		Title:       "Fix crash",
		Description: "",
		Type:        "bug",
		Label:       "not_ready",
		RepoID:      repo.ID,
		WorkflowID:  *repo.WorkflowID,
		Attachments: "[]",
		Source:      source,
		SourceRef:   sourceRef,
	})
	if err != nil {
		t.Fatalf("create sourced task: %v", err)
	}
	return task
}

func TestEligible_KnownSources(t *testing.T) {
	repoWbOn := gen.Repo{IssueWritebackEnabled: 1}
	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{"github", "github", true},
		{"gitea", "gitea", true},
		{"unknown source", "some-other-tracker", false},
		{"empty source", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := gen.Task{Source: tc.source, SourceRef: "acme/widgets#1"}
			_, _, ok := eligible(task, repoWbOn)
			if ok != tc.want {
				t.Errorf("eligible() ok = %v, want %v", ok, tc.want)
			}
		})
	}
}

// TestGiteaSourcedTask_RoutesThroughRepoForge verifies a task whose source is
// "gitea" (not "github") is write-backed via the forge.Forge resolved from
// the repo's remote URL (forge.ForRemote) rather than this Writeback's
// default (GitHub) functions — the core of resolveForgeForTask's per-task
// forge routing.
func TestGiteaSourcedTask_RoutesThroughRepoForge(t *testing.T) {
	fake := &fakeOtherForge{}
	forge.Register(fake)

	q := openTestDB(t)
	repo := seedRepoWithRemote(t, q, true, "https://forge.test/acme/widgets")
	task := seedSourcedTaskFrom(t, q, repo, "gitea", "acme/widgets#1")
	wb, defaultFake := newWritebackWithFake(q)

	wb.OnLeaveNotReady(context.Background(), task, repo)

	if len(fake.labelCalls) != 1 || fake.labelCalls[0] != InProgressLabel {
		t.Fatalf("expected the repo's resolved forge to receive the add-label call, got %v", fake.labelCalls)
	}
	if len(defaultFake.labelCalls) != 0 {
		t.Errorf("expected the Writeback's default (GitHub) functions to NOT be called for a gitea-sourced task, got %v", defaultFake.labelCalls)
	}

	updated, err := q.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WritebackInProgressSent == 0 {
		t.Fatal("expected writeback_in_progress_sent to be set")
	}
}

// TestGitHubSourcedTask_AlwaysUsesDefaultFunctions verifies a "github"-sourced
// task always uses this Writeback's own default functions, even when a repo
// happens to have a remote URL that some other registered forge would also
// recognise (shouldn't happen in practice, since forge.ParseRepoName is
// host-keyed, but resolveForgeForTask special-cases task.Source == "github"
// explicitly rather than relying on that).
func TestGitHubSourcedTask_AlwaysUsesDefaultFunctions(t *testing.T) {
	fake := &fakeOtherForge{}
	forge.Register(fake)

	q := openTestDB(t)
	// Deliberately NOT forge.test — a real github.com remote, so this also
	// covers task.Source == "github" against its natural remote.
	repo := seedRepo(t, q, true)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	wb, defaultFake := newWritebackWithFake(q)

	wb.OnLeaveNotReady(context.Background(), task, repo)

	if len(defaultFake.labelCalls) != 1 {
		t.Fatalf("expected the Writeback's default functions to be called, got %v", defaultFake.labelCalls)
	}
	if len(fake.labelCalls) != 0 {
		t.Errorf("expected the unrelated registered forge to NOT be called, got %v", fake.labelCalls)
	}
}
