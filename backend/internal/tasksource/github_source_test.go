package tasksource

import (
	"context"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestGitHubIssues_Name(t *testing.T) {
	if got, want := (GitHubIssues{}).Name(), "github"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

// TestGitHubIssues_Fetch_NoRemoteURL verifies Fetch rejects a repo with no
// configured remote URL before ever shelling out to `gh` — pure validation.
func TestGitHubIssues_Fetch_NoRemoteURL(t *testing.T) {
	repo := gen.Repo{Name: "no-remote"}
	_, err := (GitHubIssues{}).Fetch(context.Background(), repo)
	if err == nil {
		t.Fatal("expected an error for a repo with no remote URL")
	}
}

// TestGitHubIssues_Fetch_NonGitHubRemote verifies Fetch rejects a repo whose
// remote isn't a GitHub URL before shelling out — pure validation.
func TestGitHubIssues_Fetch_NonGitHubRemote(t *testing.T) {
	remote := "https://gitlab.com/example/repo.git"
	repo := gen.Repo{Name: "gitlab-repo", RemoteUrl: &remote}
	_, err := (GitHubIssues{}).Fetch(context.Background(), repo)
	if err == nil {
		t.Fatal("expected an error for a non-GitHub remote URL")
	}
}

// TestGitHubIssues_FetchComments_InvalidRef verifies FetchComments rejects a
// source ref that isn't a "owner/repo#N" github issue ref before shelling out.
func TestGitHubIssues_FetchComments_InvalidRef(t *testing.T) {
	_, err := (GitHubIssues{}).FetchComments(context.Background(), gen.Repo{Name: "r"}, "not-a-valid-ref")
	if err == nil {
		t.Fatal("expected an error for an invalid source ref")
	}
}
