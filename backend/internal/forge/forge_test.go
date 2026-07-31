package forge_test

import (
	"context"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
)

// fakeForge is a minimal forge.Forge implementation used to exercise the
// selection registry without depending on any real implementation package
// (which would risk an import cycle from this external test package back
// into e.g. ghclient).
type fakeForge struct {
	host string
}

func (f fakeForge) ParseRepoName(remoteURL string) (string, bool) {
	prefix := "https://" + f.host + "/"
	if len(remoteURL) > len(prefix) && remoteURL[:len(prefix)] == prefix {
		return remoteURL[len(prefix):], true
	}
	return "", false
}

func (fakeForge) PRForBranch(ctx context.Context, repoName, branch string) (string, string, int, error) {
	return "", "", 0, nil
}
func (fakeForge) CreatePR(ctx context.Context, repoName, branch, base, title, body string) (string, string, error) {
	return "", "", nil
}
func (fakeForge) PRHead(ctx context.Context, repoName, branch string) (forge.PRHead, error) {
	return forge.PRHead{}, nil
}
func (fakeForge) PRReviews(ctx context.Context, repoName string, prNumber int) ([]forge.Review, error) {
	return nil, nil
}
func (fakeForge) PRReviewComments(ctx context.Context, repoName string, prNumber int) ([]forge.PRReviewComment, error) {
	return nil, nil
}
func (fakeForge) FailedChecks(ctx context.Context, repoName string, prNumber int) ([]forge.Check, error) {
	return nil, nil
}
func (fakeForge) ListOpenIssues(ctx context.Context, repoName, label string) ([]forge.Issue, error) {
	return nil, nil
}
func (fakeForge) GetIssueComments(ctx context.Context, repoName string, issueNumber int) ([]forge.IssueComment, error) {
	return nil, nil
}
func (fakeForge) AddIssueLabel(ctx context.Context, repoName string, issueNumber int, label string) error {
	return nil
}
func (fakeForge) CommentOnIssue(ctx context.Context, repoName string, issueNumber int, body string) error {
	return nil
}
func (fakeForge) CloseIssueWithComment(ctx context.Context, repoName string, issueNumber int, body string) error {
	return nil
}
func (fakeForge) AuthStatus() (bool, string) { return true, "fake" }
func (fakeForge) CompareURL(repoName, base, branch, title, body string) string {
	return "https://example.invalid/" + repoName
}

var _ forge.Forge = fakeForge{}

func TestForRemote_SelectsFirstMatchingRegisteredForge(t *testing.T) {
	// Registering here (rather than relying on a real implementation's
	// init()) keeps this test self-contained regardless of which real
	// forges are compiled in.
	forge.Register(fakeForge{host: "example.test"})

	f, name, ok := forge.ForRemote("https://example.test/org/repo")
	if !ok {
		t.Fatal("expected a match for a registered fake forge's host")
	}
	if name != "org/repo" {
		t.Errorf("repo name = %q, want %q", name, "org/repo")
	}
	if _, isFake := f.(fakeForge); !isFake {
		t.Errorf("expected the fake forge to be returned, got %T", f)
	}
}

func TestForRemote_NoMatch(t *testing.T) {
	_, _, ok := forge.ForRemote("https://totally-unregistered.invalid/org/repo")
	if ok {
		t.Fatal("expected no match for an unregistered host")
	}
}

func TestMergeability_Constants(t *testing.T) {
	if forge.MergeableUnknown == forge.MergeableClean || forge.MergeableUnknown == forge.MergeableConflicting {
		t.Fatal("MergeableUnknown must be distinct from the other two states")
	}
}
