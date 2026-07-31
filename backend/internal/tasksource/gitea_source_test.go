package tasksource

import (
	"context"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestGiteaIssues_Name(t *testing.T) {
	if got, want := (GiteaIssues{}).Name(), "gitea"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

// TestGiteaIssues_AppliesTo_NoGiteaConfigured verifies AppliesTo (and by
// extension Fetch) never match any repo when no Gitea forge is registered
// (GITEA_HOST unset in this test process) — GiteaIssues is inert rather than
// erroring in that configuration, so it's always safe to include in the
// configured source list (see NewMulti's doc comment).
func TestGiteaIssues_AppliesTo_NoGiteaConfigured(t *testing.T) {
	remote := "https://git.example.com/acme/widgets"
	repo := gen.Repo{Name: "r", RemoteUrl: &remote}
	if (GiteaIssues{}).AppliesTo(repo) {
		t.Fatal("expected AppliesTo to be false with no Gitea forge registered")
	}
}

// TestGiteaIssues_Fetch_NoRemoteURL verifies Fetch rejects a repo with no
// configured remote URL before ever making a network call — pure validation,
// mirroring TestGitHubIssues_Fetch_NoRemoteURL.
func TestGiteaIssues_Fetch_NoRemoteURL(t *testing.T) {
	repo := gen.Repo{Name: "no-remote"}
	_, err := (GiteaIssues{}).Fetch(context.Background(), repo)
	if err == nil {
		t.Fatal("expected an error for a repo with no remote URL")
	}
}

// TestGiteaIssues_Fetch_UnrecognisedRemote verifies Fetch rejects a repo
// whose remote isn't recognised by any registered Gitea forge (in this test
// process, none is registered at all).
func TestGiteaIssues_Fetch_UnrecognisedRemote(t *testing.T) {
	remote := "https://git.example.com/acme/widgets"
	repo := gen.Repo{Name: "r", RemoteUrl: &remote}
	_, err := (GiteaIssues{}).Fetch(context.Background(), repo)
	if err == nil {
		t.Fatal("expected an error when no registered Gitea forge recognises the remote")
	}
}

// TestGiteaIssues_FetchComments_NoRemoteURL mirrors
// TestGiteaIssues_Fetch_NoRemoteURL for FetchComments.
func TestGiteaIssues_FetchComments_NoRemoteURL(t *testing.T) {
	repo := gen.Repo{Name: "no-remote"}
	_, err := (GiteaIssues{}).FetchComments(context.Background(), repo, "acme/widgets#1")
	if err == nil {
		t.Fatal("expected an error for a repo with no remote URL")
	}
}
