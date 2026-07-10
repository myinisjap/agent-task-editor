package ghclient

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeCmd implements ghRunner with a canned response.
type fakeCmd struct {
	output []byte
	err    error
}

func (f *fakeCmd) Output() ([]byte, error)         { return f.output, f.err }
func (f *fakeCmd) CombinedOutput() ([]byte, error) { return f.output, f.err }
func (f *fakeCmd) Run() error                      { return f.err }

// scriptedRunner installs a queue of canned responses, consumed in call
// order. Each entry may optionally assert on the args it expects to see.
// If more calls happen than were scripted, the test fails loudly instead of
// silently returning zero values, so a missing script entry is caught.
func scriptedRunner(t *testing.T, script []func(t *testing.T, args []string) fakeCmd) {
	t.Helper()
	orig := runGH
	i := 0
	runGH = func(_ context.Context, args ...string) ghRunner {
		if i >= len(script) {
			t.Fatalf("unexpected extra gh call #%d with args %v", i+1, args)
		}
		fc := script[i](t, args)
		i++
		return &fc
	}
	t.Cleanup(func() {
		runGH = orig
		if i != len(script) {
			t.Fatalf("expected %d gh calls, got %d", len(script), i)
		}
	})
}

func argsContain(args []string, needle string) bool {
	for _, a := range args {
		if a == needle {
			return true
		}
	}
	return false
}

func TestGetPRForBranch_StateNormalization(t *testing.T) {
	cases := []struct {
		name       string
		json       string
		wantState  string
		wantURL    string
		wantNumber int
	}{
		{
			name:       "open",
			json:       `[{"state":"OPEN","number":1,"url":"https://github.com/acme/widgets/pull/1"}]`,
			wantState:  "pr_open",
			wantURL:    "https://github.com/acme/widgets/pull/1",
			wantNumber: 1,
		},
		{
			name:       "merged",
			json:       `[{"state":"MERGED","number":2,"url":"https://github.com/acme/widgets/pull/2"}]`,
			wantState:  "pr_merged",
			wantURL:    "https://github.com/acme/widgets/pull/2",
			wantNumber: 2,
		},
		{
			name:       "closed",
			json:       `[{"state":"CLOSED","number":3,"url":"https://github.com/acme/widgets/pull/3"}]`,
			wantState:  "pr_closed",
			wantURL:    "https://github.com/acme/widgets/pull/3",
			wantNumber: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
				func(t *testing.T, args []string) fakeCmd {
					if !argsContain(args, "list") {
						t.Fatalf("expected pr list call, got %v", args)
					}
					return fakeCmd{output: []byte(tc.json)}
				},
			})

			state, url, num, err := GetPRForBranch(context.Background(), "acme/widgets", "some-branch")
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if state != tc.wantState {
				t.Errorf("state = %q, want %q", state, tc.wantState)
			}
			if url != tc.wantURL {
				t.Errorf("url = %q, want %q", url, tc.wantURL)
			}
			if num != tc.wantNumber {
				t.Errorf("number = %d, want %d", num, tc.wantNumber)
			}
		})
	}
}

func TestGetPRForBranch_NoPR_BranchExists(t *testing.T) {
	scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
		func(t *testing.T, args []string) fakeCmd {
			return fakeCmd{output: []byte(`[]`)}
		},
		func(t *testing.T, args []string) fakeCmd {
			if !argsContain(args, "api") {
				t.Fatalf("expected branch-check call, got %v", args)
			}
			return fakeCmd{err: nil}
		},
	})

	state, url, num, err := GetPRForBranch(context.Background(), "acme/widgets", "some-branch")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if state != "pushed" {
		t.Errorf("state = %q, want %q", state, "pushed")
	}
	if url != "" || num != 0 {
		t.Errorf("url/num = %q/%d, want empty/0", url, num)
	}
}

func TestGetPRForBranch_NoPR_BranchNotExists(t *testing.T) {
	scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
		func(t *testing.T, args []string) fakeCmd {
			return fakeCmd{output: []byte(`[]`)}
		},
		func(t *testing.T, args []string) fakeCmd {
			return fakeCmd{err: errors.New("404")}
		},
	})

	state, url, num, err := GetPRForBranch(context.Background(), "acme/widgets", "some-branch")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if state != "" {
		t.Errorf("state = %q, want empty", state)
	}
	if url != "" || num != 0 {
		t.Errorf("url/num = %q/%d, want empty/0", url, num)
	}
}

func TestCreatePR_ExistingPRShortCircuit(t *testing.T) {
	scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
		func(t *testing.T, args []string) fakeCmd {
			if !argsContain(args, "list") {
				t.Fatalf("expected pr list call, got %v", args)
			}
			return fakeCmd{output: []byte(`[{"state":"OPEN","number":7,"url":"https://github.com/acme/widgets/pull/7"}]`)}
		},
	})

	state, url, err := CreatePR(context.Background(), "acme/widgets", "some-branch", "main", "title", "body")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if state != "pr_open" {
		t.Errorf("state = %q, want pr_open", state)
	}
	if url != "https://github.com/acme/widgets/pull/7" {
		t.Errorf("url = %q, want pull/7", url)
	}
}

func TestCreatePR_CreatesNew(t *testing.T) {
	scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
		// Idempotency pre-check: pr list -> empty
		func(t *testing.T, args []string) fakeCmd {
			if !argsContain(args, "list") {
				t.Fatalf("expected pr list call, got %v", args)
			}
			return fakeCmd{output: []byte(`[]`)}
		},
		// GetPRForBranch's follow-up branch-check since PR list was empty.
		func(t *testing.T, args []string) fakeCmd {
			if !argsContain(args, "api") {
				t.Fatalf("expected branch-check call, got %v", args)
			}
			return fakeCmd{err: errors.New("404")}
		},
		// pr create
		func(t *testing.T, args []string) fakeCmd {
			if !argsContain(args, "create") {
				t.Fatalf("expected pr create call, got %v", args)
			}
			return fakeCmd{output: []byte("Creating pull request\nhttps://github.com/org/repo/pull/5\n")}
		},
	})

	state, url, err := CreatePR(context.Background(), "acme/widgets", "some-branch", "main", "title", "body")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if state != "pr_open" {
		t.Errorf("state = %q, want pr_open", state)
	}
	if url != "https://github.com/org/repo/pull/5" {
		t.Errorf("url = %q, want pull/5", url)
	}
}

func TestCreatePR_RaceAlreadyExists(t *testing.T) {
	scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
		// Idempotency pre-check: pr list -> empty
		func(t *testing.T, args []string) fakeCmd {
			return fakeCmd{output: []byte(`[]`)}
		},
		// GetPRForBranch's follow-up branch-check since PR list was empty.
		func(t *testing.T, args []string) fakeCmd {
			return fakeCmd{err: errors.New("404")}
		},
		// pr create fails with "already exists"
		func(t *testing.T, args []string) fakeCmd {
			if !argsContain(args, "create") {
				t.Fatalf("expected pr create call, got %v", args)
			}
			return fakeCmd{output: []byte("a pull request for branch \"some-branch\" already exists"), err: errors.New("exit status 1")}
		},
		// Re-check pr list -> now has a PR
		func(t *testing.T, args []string) fakeCmd {
			if !argsContain(args, "list") {
				t.Fatalf("expected re-check pr list call, got %v", args)
			}
			return fakeCmd{output: []byte(`[{"state":"OPEN","number":9,"url":"https://github.com/acme/widgets/pull/9"}]`)}
		},
	})

	state, url, err := CreatePR(context.Background(), "acme/widgets", "some-branch", "main", "title", "body")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if state != "pr_open" {
		t.Errorf("state = %q, want pr_open", state)
	}
	if url != "https://github.com/acme/widgets/pull/9" {
		t.Errorf("url = %q, want pull/9", url)
	}
}

func TestListOpenIssues_LabelFiltering(t *testing.T) {
	t.Run("no label", func(t *testing.T) {
		var capturedArgs []string
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				capturedArgs = args
				return fakeCmd{output: []byte(`[{"number":1,"title":"t1","body":"b1","url":"u1","labels":[{"name":"bug"},{"name":"urgent"}]}]`)}
			},
		})

		issues, err := ListOpenIssues(context.Background(), "acme/widgets", "")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if argsContain(capturedArgs, "--label") {
			t.Errorf("did not expect --label flag in args: %v", capturedArgs)
		}
		if len(issues) != 1 {
			t.Fatalf("expected 1 issue, got %d", len(issues))
		}
		if got, want := issues[0].Labels, []string{"bug", "urgent"}; !equalStrSlices(got, want) {
			t.Errorf("labels = %v, want %v", got, want)
		}
	})

	t.Run("with label", func(t *testing.T) {
		var capturedArgs []string
		scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
			func(t *testing.T, args []string) fakeCmd {
				capturedArgs = args
				return fakeCmd{output: []byte(`[]`)}
			},
		})

		_, err := ListOpenIssues(context.Background(), "acme/widgets", "bug")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !argsContain(capturedArgs, "--label") {
			t.Errorf("expected --label flag in args: %v", capturedArgs)
		}
		if !argsContain(capturedArgs, "bug") {
			t.Errorf("expected label value 'bug' in args: %v", capturedArgs)
		}
	})
}

func TestAddIssueLabel(t *testing.T) {
	scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
		func(t *testing.T, args []string) fakeCmd {
			if !argsContain(args, "edit") || !argsContain(args, "--add-label") || !argsContain(args, "agent-in-progress") {
				t.Fatalf("expected issue edit --add-label call, got %v", args)
			}
			if !argsContain(args, "--repo") || !argsContain(args, "acme/widgets") {
				t.Fatalf("expected --repo acme/widgets in args, got %v", args)
			}
			return fakeCmd{output: []byte("")}
		},
	})

	if err := AddIssueLabel(context.Background(), "acme/widgets", 42, "agent-in-progress"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestAddIssueLabel_Error(t *testing.T) {
	scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
		func(t *testing.T, args []string) fakeCmd {
			return fakeCmd{output: []byte("label not found"), err: errors.New("exit status 1")}
		},
	})

	if err := AddIssueLabel(context.Background(), "acme/widgets", 42, "does-not-exist"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCommentOnIssue(t *testing.T) {
	scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
		func(t *testing.T, args []string) fakeCmd {
			if !argsContain(args, "comment") || !argsContain(args, "--body") {
				t.Fatalf("expected issue comment --body call, got %v", args)
			}
			return fakeCmd{output: []byte("https://github.com/acme/widgets/issues/42#issuecomment-1")}
		},
	})

	if err := CommentOnIssue(context.Background(), "acme/widgets", 42, "PR opened: https://github.com/acme/widgets/pull/7"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestCommentOnIssue_Error(t *testing.T) {
	scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
		func(t *testing.T, args []string) fakeCmd {
			return fakeCmd{output: []byte("not found"), err: errors.New("exit status 1")}
		},
	})

	if err := CommentOnIssue(context.Background(), "acme/widgets", 42, "body"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCloseIssueWithComment(t *testing.T) {
	scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
		func(t *testing.T, args []string) fakeCmd {
			if !argsContain(args, "close") || !argsContain(args, "--comment") {
				t.Fatalf("expected issue close --comment call, got %v", args)
			}
			return fakeCmd{output: []byte("")}
		},
	})

	if err := CloseIssueWithComment(context.Background(), "acme/widgets", 42, "merged: https://github.com/acme/widgets/pull/7"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestCloseIssueWithComment_Error(t *testing.T) {
	scriptedRunner(t, []func(t *testing.T, args []string) fakeCmd{
		func(t *testing.T, args []string) fakeCmd {
			return fakeCmd{output: []byte("already closed"), err: errors.New("exit status 1")}
		},
	})

	if err := CloseIssueWithComment(context.Background(), "acme/widgets", 42, "body"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func equalStrSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParseGitHubName(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		want   string
		wantOK bool
	}{
		{"https", "https://github.com/org/repo", "org/repo", true},
		{"https with .git", "https://github.com/org/repo.git", "org/repo", true},
		{"ssh", "git@github.com:org/repo", "org/repo", true},
		{"ssh with .git", "git@github.com:org/repo.git", "org/repo", true},
		{"empty", "", "", false},
		{"junk", "not a url", "", false},
		{"gitlab", "https://gitlab.com/org/repo", "", false},
		{"missing repo", "https://github.com/onlyorg", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseGitHubName(tc.url)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("name = %q, want %q", got, tc.want)
			}
		})
	}
}

// Sanity check that our scriptedRunner's arg-join helper is usable elsewhere
// if needed for debugging failures.
func TestArgsContainHelper(t *testing.T) {
	if !argsContain([]string{"a", "b"}, "b") {
		t.Fatal("expected argsContain to find b")
	}
	if argsContain([]string{"a", "b"}, "c") {
		t.Fatal("expected argsContain to not find c")
	}
	_ = strings.Join([]string{"a"}, " ")
}
