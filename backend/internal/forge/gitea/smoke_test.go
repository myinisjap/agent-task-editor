package gitea

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSmokeLiveInstance is an opt-in integration test that exercises the
// Gitea forge implementation against a real, self-hosted Gitea instance
// (rather than the httptest fakes used everywhere else in this package).
//
// It is skipped by default so `go test ./...` and CI never require (or
// depend on) a live Gitea instance. To run it locally:
//
//	GITEA_SMOKE=1 \
//	GITEA_HOST=git.example.com \
//	GITEA_TOKEN=<token> \
//	GITEA_SMOKE_REPO=owner/repo \
//	go test ./internal/forge/gitea/... -run TestSmokeLiveInstance -v
//
// Only read-only, side-effect-free operations are exercised (AuthStatus,
// ParseRepoName, ListOpenIssues, GetIssueComments, PRForBranch/PRHead) so
// this never mutates the target instance. See docs/task-sources.md for more
// on the required env vars.
func TestSmokeLiveInstance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live-instance smoke test in -short mode")
	}
	if os.Getenv("GITEA_SMOKE") == "" {
		t.Skip("set GITEA_SMOKE=1 (plus GITEA_HOST/GITEA_TOKEN/GITEA_SMOKE_REPO) to run this opt-in smoke test against a real Gitea instance")
	}

	g, ok := New()
	if !ok {
		t.Skip("GITEA_HOST not set; cannot construct a live Gitea forge")
	}

	repoName := strings.TrimSpace(os.Getenv("GITEA_SMOKE_REPO"))
	if repoName == "" {
		t.Skip("set GITEA_SMOKE_REPO=owner/repo to point the smoke test at a repo on the live instance")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("AuthStatus", func(t *testing.T) {
		authed, note := g.AuthStatus()
		if !authed {
			t.Fatalf("expected AuthStatus to report authenticated (using GITEA_TOKEN), got note: %q", note)
		}
		if note == "" {
			t.Error("expected a non-empty note describing auth status")
		}
	})

	t.Run("ParseRepoName", func(t *testing.T) {
		remote := "https://" + g.hosts[0] + "/" + repoName + ".git"
		name, ok := g.ParseRepoName(remote)
		if !ok {
			t.Fatalf("ParseRepoName(%q) returned ok=false, want true", remote)
		}
		if name != repoName {
			t.Errorf("ParseRepoName(%q) = %q, want %q", remote, name, repoName)
		}
	})

	openIssues, err := g.ListOpenIssues(ctx, repoName, "")
	if err != nil {
		t.Fatalf("ListOpenIssues(%q) failed: %v", repoName, err)
	}
	t.Logf("ListOpenIssues(%q) returned %d open issue(s)", repoName, len(openIssues))

	if len(openIssues) > 0 {
		issue := openIssues[0]
		if issue.Number == 0 {
			t.Error("expected first open issue to have a non-zero number")
		}

		t.Run("GetIssueComments", func(t *testing.T) {
			comments, err := g.GetIssueComments(ctx, repoName, issue.Number)
			if err != nil {
				t.Fatalf("GetIssueComments(%q, %d) failed: %v", repoName, issue.Number, err)
			}
			t.Logf("issue #%d has %d comment(s)", issue.Number, len(comments))
		})
	} else {
		t.Log("repo has no open issues; skipping GetIssueComments sub-check")
	}

	// PRForBranch/PRHead against a branch that may or may not exist — this is
	// read-only regardless of the outcome, so just confirm it doesn't error
	// (an empty state/zero PRHead is a legitimate "no PR for this branch"
	// answer, not a failure).
	branch := strings.TrimSpace(os.Getenv("GITEA_SMOKE_BRANCH"))
	if branch == "" {
		branch = "main"
	}
	t.Run("PRForBranch", func(t *testing.T) {
		state, prURL, prNumber, err := g.PRForBranch(ctx, repoName, branch)
		if err != nil {
			t.Fatalf("PRForBranch(%q, %q) failed: %v", repoName, branch, err)
		}
		t.Logf("PRForBranch(%q, %q) = state=%q url=%q number=%d", repoName, branch, state, prURL, prNumber)
	})
	t.Run("PRHead", func(t *testing.T) {
		head, err := g.PRHead(ctx, repoName, branch)
		if err != nil {
			t.Fatalf("PRHead(%q, %q) failed: %v", repoName, branch, err)
		}
		t.Logf("PRHead(%q, %q) = %+v", repoName, branch, head)
	})
}
