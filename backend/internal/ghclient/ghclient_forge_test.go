package ghclient

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
)

// argAfter returns the value immediately following the first occurrence of
// flag in args, or "" if flag is absent or is the last element. Unlike
// argsContain, this pins a flag to its adjacent value, which is what catches
// an argument-order swap (e.g. passing branch where base was expected).
func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// TestGitHubForgeDelegation drives every forge.Forge method through a
// forge.Forge-typed variable (not the concrete GitHub type) so the interface
// assertion itself is exercised, and checks that each argument lands in its
// expected position in the underlying gh invocation — catching an
// argument-order swap in GitHub's one-line delegations, which would
// otherwise be invisible to the rest of the suite.
func TestGitHubForgeDelegation(t *testing.T) {
	var f forge.Forge = GitHub{}

	t.Run("ParseRepoName", func(t *testing.T) {
		name, ok := f.ParseRepoName("https://github.com/acme/widgets")
		if !ok || name != "acme/widgets" {
			t.Fatalf("ParseRepoName = (%q, %v), want (acme/widgets, true)", name, ok)
		}
	})

	t.Run("PRForBranch", func(t *testing.T) {
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				if got := argAfter(args, "--repo"); got != "acme/widgets" {
					t.Fatalf("--repo = %q, want acme/widgets (args: %v)", got, args)
				}
				if got := argAfter(args, "--head"); got != "feat-x" {
					t.Fatalf("--head = %q, want feat-x (args: %v)", got, args)
				}
				return fakeCmd{output: []byte(`[{"state":"OPEN","number":11,"url":"https://github.com/acme/widgets/pull/11"}]`)}
			},
		})

		state, url, num, err := f.PRForBranch(context.Background(), "acme/widgets", "feat-x")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if state != "pr_open" || url != "https://github.com/acme/widgets/pull/11" || num != 11 {
			t.Fatalf("PRForBranch = (%q, %q, %d), want (pr_open, .../pull/11, 11)", state, url, num)
		}
	})

	t.Run("CreatePR_existing", func(t *testing.T) {
		// CreatePR's idempotency pre-check (GetPRForBranch) finds an existing
		// PR, so it short-circuits without ever calling `gh pr create` —
		// script only the one call.
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				if got := argAfter(args, "--repo"); got != "acme/widgets" {
					t.Fatalf("--repo = %q, want acme/widgets (args: %v)", got, args)
				}
				if got := argAfter(args, "--head"); got != "feat-x" {
					t.Fatalf("--head = %q, want feat-x (args: %v)", got, args)
				}
				return fakeCmd{output: []byte(`[{"state":"OPEN","number":21,"url":"https://github.com/acme/widgets/pull/21"}]`)}
			},
		})

		state, url, err := f.CreatePR(context.Background(), "acme/widgets", "feat-x", "main", "T", "B")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if state != "pr_open" || url != "https://github.com/acme/widgets/pull/21" {
			t.Fatalf("CreatePR = (%q, %q), want (pr_open, .../pull/21)", state, url)
		}
	})

	t.Run("CreatePR_new", func(t *testing.T) {
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			// Idempotency pre-check: pr list -> empty.
			func(t *testing.T, args []string) fakeCmd {
				if !argsContain(args, "list") {
					t.Fatalf("expected pr list call, got %v", args)
				}
				return fakeCmd{output: []byte(`[]`)}
			},
			// GetPRForBranch's follow-up branch-check since the list was empty.
			func(t *testing.T, args []string) fakeCmd {
				if !argsContain(args, "api") {
					t.Fatalf("expected branch-check call, got %v", args)
				}
				return fakeCmd{err: errors.New("404")}
			},
			// pr create — assert every positional arg lands in the right flag,
			// using distinct fixture values so a swap between any two would fail.
			func(t *testing.T, args []string) fakeCmd {
				if !argsContain(args, "create") {
					t.Fatalf("expected pr create call, got %v", args)
				}
				if got := argAfter(args, "--repo"); got != "acme/widgets" {
					t.Fatalf("--repo = %q, want acme/widgets (args: %v)", got, args)
				}
				if got := argAfter(args, "--head"); got != "feat-x" {
					t.Fatalf("--head = %q, want feat-x (args: %v)", got, args)
				}
				if got := argAfter(args, "--base"); got != "main" {
					t.Fatalf("--base = %q, want main (args: %v)", got, args)
				}
				if got := argAfter(args, "--title"); got != "T" {
					t.Fatalf("--title = %q, want T (args: %v)", got, args)
				}
				if got := argAfter(args, "--body"); got != "B" {
					t.Fatalf("--body = %q, want B (args: %v)", got, args)
				}
				return fakeCmd{output: []byte("Creating pull request\nhttps://github.com/acme/widgets/pull/31\n")}
			},
		})

		state, url, err := f.CreatePR(context.Background(), "acme/widgets", "feat-x", "main", "T", "B")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if state != "pr_open" || url != "https://github.com/acme/widgets/pull/31" {
			t.Fatalf("CreatePR = (%q, %q), want (pr_open, .../pull/31)", state, url)
		}
	})

	t.Run("PRHead", func(t *testing.T) {
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				if got := argAfter(args, "--repo"); got != "acme/widgets" {
					t.Fatalf("--repo = %q, want acme/widgets (args: %v)", got, args)
				}
				if got := argAfter(args, "--head"); got != "feat-x" {
					t.Fatalf("--head = %q, want feat-x (args: %v)", got, args)
				}
				return fakeCmd{output: []byte(`[{"number":41,"headRefOid":"deadbeef","baseRefName":"main","mergeable":"MERGEABLE"}]`)}
			},
		})

		head, err := f.PRHead(context.Background(), "acme/widgets", "feat-x")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := forge.PRHead{Number: 41, HeadSHA: "deadbeef", BaseRef: "main", Mergeable: forge.MergeableClean}
		if head != want {
			t.Fatalf("PRHead = %+v, want %+v", head, want)
		}
	})

	t.Run("PRReviews", func(t *testing.T) {
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				if !argsContain(args, "51") {
					t.Fatalf("expected PR number 51 in args, got %v", args)
				}
				if got := argAfter(args, "--repo"); got != "acme/widgets" {
					t.Fatalf("--repo = %q, want acme/widgets (args: %v)", got, args)
				}
				return fakeCmd{output: []byte(`{"reviews":[{"id":"r1","state":"approved","body":"lgtm","author":{"login":"alice"},"submittedAt":"2024-01-01T00:00:00Z"}]}`)}
			},
		})

		reviews, err := f.PRReviews(context.Background(), "acme/widgets", 51)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := []forge.Review{{ID: "r1", State: "APPROVED", Body: "lgtm", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"}}
		if len(reviews) != 1 || reviews[0] != want[0] {
			t.Fatalf("PRReviews = %+v, want %+v", reviews, want)
		}
	})

	t.Run("PRReviewComments", func(t *testing.T) {
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				if !argsContain(args, "repos/acme/widgets/pulls/61/comments") {
					t.Fatalf("expected pulls/61/comments path in args, got %v", args)
				}
				return fakeCmd{output: []byte(`[{"id":1,"path":"a.go","line":5,"side":"RIGHT","body":"nit","diff_hunk":"@@","commit_id":"abc","user":{"login":"bob"},"created_at":"2024-01-02T00:00:00Z"}]`)}
			},
		})

		comments, err := f.PRReviewComments(context.Background(), "acme/widgets", 61)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(comments) != 1 || comments[0].Path != "a.go" || comments[0].Author != "bob" {
			t.Fatalf("PRReviewComments = %+v, unexpected content", comments)
		}
	})

	t.Run("FailedChecks", func(t *testing.T) {
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				if !argsContain(args, "71") {
					t.Fatalf("expected PR number 71 in args, got %v", args)
				}
				if got := argAfter(args, "--repo"); got != "acme/widgets" {
					t.Fatalf("--repo = %q, want acme/widgets (args: %v)", got, args)
				}
				return fakeCmd{output: []byte(`[{"name":"build","link":"https://ci/1","bucket":"fail"},{"name":"lint","link":"https://ci/2","bucket":"pass"}]`)}
			},
		})

		checks, err := f.FailedChecks(context.Background(), "acme/widgets", 71)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(checks) != 1 || checks[0].Name != "build" {
			t.Fatalf("FailedChecks = %+v, want only the failing build check", checks)
		}
	})

	t.Run("ListOpenIssues", func(t *testing.T) {
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				joined := strings.Join(args, " ")
				if !strings.Contains(joined, "repos/acme/widgets/issues?") || !strings.Contains(joined, "labels=bug") {
					t.Fatalf("expected repos/acme/widgets/issues? path with labels=bug, got %v", args)
				}
				return fakeCmd{output: []byte(`[{"number":81,"title":"t","body":"b","html_url":"u","author_association":"OWNER","labels":[{"name":"bug"}]}]`)}
			},
		})

		issues, err := f.ListOpenIssues(context.Background(), "acme/widgets", "bug")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(issues) != 1 || issues[0].Number != 81 {
			t.Fatalf("ListOpenIssues = %+v, want a single issue #81", issues)
		}
	})

	t.Run("GetIssueComments", func(t *testing.T) {
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				if !argsContain(args, "repos/acme/widgets/issues/91/comments") {
					t.Fatalf("expected issues/91/comments path in args, got %v", args)
				}
				return fakeCmd{output: []byte(`[{"id":1,"body":"c1","author_association":"MEMBER","user":{"login":"carol"},"created_at":"2024-01-03T00:00:00Z"}]`)}
			},
		})

		comments, err := f.GetIssueComments(context.Background(), "acme/widgets", 91)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(comments) != 1 || comments[0].Author != "carol" {
			t.Fatalf("GetIssueComments = %+v, unexpected content", comments)
		}
	})

	t.Run("AddIssueLabel", func(t *testing.T) {
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				if !argsContain(args, "101") {
					t.Fatalf("expected issue number 101 in args, got %v", args)
				}
				if got := argAfter(args, "--repo"); got != "acme/widgets" {
					t.Fatalf("--repo = %q, want acme/widgets (args: %v)", got, args)
				}
				if got := argAfter(args, "--add-label"); got != "agent-in-progress" {
					t.Fatalf("--add-label = %q, want agent-in-progress (args: %v)", got, args)
				}
				return fakeCmd{output: []byte("")}
			},
		})

		if err := f.AddIssueLabel(context.Background(), "acme/widgets", 101, "agent-in-progress"); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("CommentOnIssue", func(t *testing.T) {
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				if !argsContain(args, "111") {
					t.Fatalf("expected issue number 111 in args, got %v", args)
				}
				if got := argAfter(args, "--repo"); got != "acme/widgets" {
					t.Fatalf("--repo = %q, want acme/widgets (args: %v)", got, args)
				}
				if got := argAfter(args, "--body"); got != "hello there" {
					t.Fatalf("--body = %q, want %q (args: %v)", got, "hello there", args)
				}
				return fakeCmd{output: []byte("")}
			},
		})

		if err := f.CommentOnIssue(context.Background(), "acme/widgets", 111, "hello there"); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("CloseIssueWithComment", func(t *testing.T) {
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				if !argsContain(args, "121") {
					t.Fatalf("expected issue number 121 in args, got %v", args)
				}
				if got := argAfter(args, "--repo"); got != "acme/widgets" {
					t.Fatalf("--repo = %q, want acme/widgets (args: %v)", got, args)
				}
				if got := argAfter(args, "--comment"); got != "closing now" {
					t.Fatalf("--comment = %q, want %q (args: %v)", got, "closing now", args)
				}
				return fakeCmd{output: []byte("")}
			},
		})

		if err := f.CloseIssueWithComment(context.Background(), "acme/widgets", 121, "closing now"); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}

// TestGitHubName pins GitHub.Name()'s return value, which is relied on
// elsewhere (e.g. tasksource.GitHubIssues.Name() and ghsync's PR-review-
// comment source tagging) to match exactly "github".
func TestGitHubName(t *testing.T) {
	if got := (GitHub{}).Name(); got != "github" {
		t.Errorf("Name() = %q, want %q", got, "github")
	}
}

// TestGitHubCompareURL asserts the full constructed URL string, since URL
// construction (via url.Values.Encode(), which sorts keys alphabetically:
// body, expand, title) is CompareURL's only real logic.
func TestGitHubCompareURL(t *testing.T) {
	cases := []struct {
		name                   string
		repoName, base, branch string
		title, body            string
		want                   string
	}{
		{
			name:     "simple",
			repoName: "acme/widgets",
			base:     "main",
			branch:   "feat-x",
			title:    "My Title",
			body:     "My Body",
			want:     "https://github.com/acme/widgets/compare/main...feat-x?body=My+Body&expand=1&title=My+Title",
		},
		{
			name:     "needs escaping",
			repoName: "acme/widgets",
			base:     "main",
			branch:   "feat-x",
			title:    "T&T #1\nnewline",
			body:     "B&B #2\nnewline",
			want:     "https://github.com/acme/widgets/compare/main...feat-x?body=B%26B+%232%0Anewline&expand=1&title=T%26T+%231%0Anewline",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := (GitHub{}).CompareURL(tc.repoName, tc.base, tc.branch, tc.title, tc.body)
			if got != tc.want {
				t.Errorf("CompareURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGitHubAuthStatus_TokenFallback exercises the GITHUB_TOKEN fallback
// path of AuthStatus. AuthStatus shells out via a real exec.Command (not the
// runGH seam), so it can't be scripted; this only pins the fallback branch,
// which is reachable regardless of whether `gh` itself is installed or
// authenticated on the machine running the test. It does not assert on the
// primary `gh auth status` path, since that may legitimately succeed on a
// dev machine with real stored credentials.
func TestGitHubAuthStatus_TokenFallback(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "x")

	authed, note := (GitHub{}).AuthStatus()

	if !authed {
		t.Errorf("AuthStatus() authed = false, want true (either via gh auth or GITHUB_TOKEN fallback); note = %q", note)
	}
	if note == "" {
		t.Errorf("AuthStatus() note is empty, want a non-empty explanation")
	}
}
