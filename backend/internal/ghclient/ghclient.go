// Package ghclient provides shared GitHub CLI helpers used by both the HTTP
// handlers and the background GitHub-sync goroutine.
package ghclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
)

// ghRunner is the subset of *exec.Cmd used by the gh-calling functions below.
// Abstracted out so tests can substitute a fake without shelling out to a
// real gh binary.
type ghRunner interface {
	Output() ([]byte, error)
	CombinedOutput() ([]byte, error)
	Run() error
}

// runGH constructs the command used to invoke the gh CLI with the given
// args. Overridable in tests.
var runGH = func(ctx context.Context, args ...string) ghRunner {
	return exec.CommandContext(ctx, "gh", args...)
}

// GHAuthStatus returns whether the gh CLI has valid auth credentials.
// Primary: runs `gh auth status` to check for stored credentials (e.g. from the
// ~/.config/gh volume mount).
// Fallback: checks the GITHUB_TOKEN env var (gh picks it up automatically).
func GHAuthStatus() (authed bool, note string) {
	metrics.GhCallsTotal.WithLabelValues("auth_status").Inc()
	cmd := exec.Command("gh", "auth", "status")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, "gh auth"
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return true, "GITHUB_TOKEN env var"
	}
	return false, strings.TrimSpace(string(out))
}

// GetPRForBranch queries GitHub for a PR matching the given branch in the given
// repo (org/repo format). Returns the normalised state ("pushed", "pr_open",
// "pr_merged", "pr_closed"), the PR web URL, the PR number, and any error.
func GetPRForBranch(ctx context.Context, repoName, branch string) (state, prURL string, prNumber int, err error) {
	metrics.GhCallsTotal.WithLabelValues("pr_list").Inc()
	cmd := runGH(ctx, "pr", "list",
		"--repo", repoName,
		"--head", branch,
		"--state", "all",
		"--json", "state,number,url",
		"--limit", "1",
	)
	out, err := cmd.Output()
	if err != nil {
		return "", "", 0, err
	}

	var prs []struct {
		State  string `json:"state"`
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return "", "", 0, err
	}

	if len(prs) == 0 {
		// No PR yet — verify the branch actually exists on the remote.
		metrics.GhCallsTotal.WithLabelValues("branch_check").Inc()
		chk := runGH(ctx, "api", "repos/"+repoName+"/branches/"+branch, "--silent")
		if chk.Run() != nil {
			return "", "", 0, nil // branch not on remote yet
		}
		return "pushed", "", 0, nil
	}

	// gh returns state as OPEN, MERGED, CLOSED (uppercase)
	s := strings.ToLower(prs[0].State)
	switch s {
	case "open":
		s = "pr_open"
	case "merged":
		s = "pr_merged"
	case "closed":
		s = "pr_closed"
	}
	return s, prs[0].URL, prs[0].Number, nil
}

// CreatePR opens a pull request for the given branch using the gh CLI, or
// returns the existing PR if one already exists for the branch (idempotent).
// title/body are only used when creating a new PR. base is the target branch
// (e.g. "main"); an empty base lets gh fall back to the repo's default branch.
// The branch is expected to already be pushed to origin.
//
// Returns the normalised state ("pr_open" for a freshly created PR, or the
// existing PR's state) and the PR web URL.
func CreatePR(ctx context.Context, repoName, branch, base, title, body string) (state, prURL string, err error) {
	// Idempotency: if a PR already exists for this branch, return it rather
	// than letting `gh pr create` fail with "a pull request already exists".
	if s, u, n, gerr := GetPRForBranch(ctx, repoName, branch); gerr == nil && n != 0 {
		return s, u, nil
	}

	args := []string{"pr", "create",
		"--repo", repoName,
		"--head", branch,
		"--title", title,
		"--body", body,
	}
	if base != "" {
		args = append(args, "--base", base)
	}
	metrics.GhCallsTotal.WithLabelValues("pr_create").Inc()
	out, err := runGH(ctx, args...).CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		// Handle the race where a PR appeared between our check and create.
		if strings.Contains(trimmed, "already exists") {
			if s, u, n, gerr := GetPRForBranch(ctx, repoName, branch); gerr == nil && n != 0 {
				return s, u, nil
			}
		}
		return "", "", fmt.Errorf("gh pr create: %w: %s", err, trimmed)
	}

	// `gh pr create` prints the new PR's URL on stdout (last non-empty line).
	url := ""
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "https://") {
			url = line
		}
	}
	return "pr_open", url, nil
}

// PRHead holds a PR's number and current head commit SHA, used by ghsync to
// detect when the agent has pushed a new commit (see GetPRHead).
type PRHead struct {
	Number  int
	HeadSHA string
}

// GetPRHead returns the PR number and head commit SHA for the given branch,
// or a zero PRHead if no PR exists yet for the branch. Used to detect a fresh
// push since the last sweep (the review/feedback ingestion cursor resets when
// the head SHA changes).
func GetPRHead(ctx context.Context, repoName, branch string) (PRHead, error) {
	metrics.GhCallsTotal.WithLabelValues("pr_list_head").Inc()
	cmd := runGH(ctx, "pr", "list",
		"--repo", repoName,
		"--head", branch,
		"--state", "all",
		"--json", "number,headRefOid",
		"--limit", "1",
	)
	out, err := cmd.Output()
	if err != nil {
		return PRHead{}, err
	}
	var prs []struct {
		Number     int    `json:"number"`
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return PRHead{}, err
	}
	if len(prs) == 0 {
		return PRHead{}, nil
	}
	return PRHead{Number: prs[0].Number, HeadSHA: prs[0].HeadRefOid}, nil
}

// Review is a single review left on a PR (a "changes requested"/"approved"/
// "commented" submission with a body, as opposed to an inline review
// comment — see PRReviewComment).
type Review struct {
	ID          string
	State       string // "APPROVED", "CHANGES_REQUESTED", "COMMENTED", etc (uppercase, as returned by gh)
	Body        string
	Author      string
	SubmittedAt string // RFC3339 timestamp string, compared lexically for cursor purposes
}

// GetPRReviews returns all reviews submitted on the given PR, in the order
// GitHub returns them (oldest first).
func GetPRReviews(ctx context.Context, repoName string, prNumber int) ([]Review, error) {
	metrics.GhCallsTotal.WithLabelValues("pr_reviews").Inc()
	cmd := runGH(ctx, "pr", "view", fmt.Sprint(prNumber),
		"--repo", repoName,
		"--json", "reviews",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var payload struct {
		Reviews []struct {
			ID     string `json:"id"`
			State  string `json:"state"`
			Body   string `json:"body"`
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			SubmittedAt string `json:"submittedAt"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, err
	}
	reviews := make([]Review, 0, len(payload.Reviews))
	for _, r := range payload.Reviews {
		reviews = append(reviews, Review{
			ID:          r.ID,
			State:       strings.ToUpper(r.State),
			Body:        r.Body,
			Author:      r.Author.Login,
			SubmittedAt: r.SubmittedAt,
		})
	}
	return reviews, nil
}

// PRReviewComment is a single inline (file/line-anchored) review comment left
// on a PR's diff. Named distinctly from agent.ReviewComment (the local
// human-left-in-app equivalent) to avoid an import cycle / naming collision.
type PRReviewComment struct {
	ID        string
	Path      string
	Line      int    // the line the comment is anchored to; 0 if the comment is on an outdated/removed diff position
	StartLine int    // for multi-line comments; equals Line when the comment spans a single line
	Side      string // "LEFT" or "RIGHT" (maps to our "old"/"new")
	Body      string
	DiffHunk  string
	CommitID  string
	Author    string
	CreatedAt string
}

// GetPRReviewComments returns all inline review comments left on the given
// PR's diff, across all reviews. Paginates through the full result set.
func GetPRReviewComments(ctx context.Context, repoName string, prNumber int) ([]PRReviewComment, error) {
	metrics.GhCallsTotal.WithLabelValues("pr_review_comments").Inc()
	cmd := runGH(ctx, "api",
		fmt.Sprintf("repos/%s/pulls/%d/comments", repoName, prNumber),
		"--paginate",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	// --paginate concatenates one JSON array per page back-to-back rather than
	// merging them into a single array, so decode with a streaming decoder that
	// consumes each array in turn.
	dec := json.NewDecoder(strings.NewReader(string(out)))
	var comments []PRReviewComment
	for dec.More() {
		var page []struct {
			ID        int64  `json:"id"`
			Path      string `json:"path"`
			Line      *int   `json:"line"`
			StartLine *int   `json:"start_line"`
			Side      string `json:"side"`
			Body      string `json:"body"`
			DiffHunk  string `json:"diff_hunk"`
			CommitID  string `json:"commit_id"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
			CreatedAt string `json:"created_at"`
		}
		if err := dec.Decode(&page); err != nil {
			return nil, err
		}
		for _, c := range page {
			line := 0
			if c.Line != nil {
				line = *c.Line
			}
			startLine := line
			if c.StartLine != nil {
				startLine = *c.StartLine
			}
			comments = append(comments, PRReviewComment{
				ID:        fmt.Sprint(c.ID),
				Path:      c.Path,
				Line:      line,
				StartLine: startLine,
				Side:      c.Side,
				Body:      c.Body,
				DiffHunk:  c.DiffHunk,
				CommitID:  c.CommitID,
				Author:    c.User.Login,
				CreatedAt: c.CreatedAt,
			})
		}
	}
	return comments, nil
}

// Check is a single GitHub Actions / status check result on a PR.
type Check struct {
	Name string
	Link string
	// Bucket is gh's coarse classification: "pass", "fail", "pending", "skipping", "cancel".
	Bucket string
}

// GetFailedChecks returns the checks on the given PR whose bucket is "fail"
// or "cancel" (build/test failures and cancelled runs — both indicate the
// agent's last push didn't pass CI). Pending/skipped/passing checks are
// excluded.
func GetFailedChecks(ctx context.Context, repoName string, prNumber int) ([]Check, error) {
	metrics.GhCallsTotal.WithLabelValues("pr_checks").Inc()
	cmd := runGH(ctx, "pr", "checks", fmt.Sprint(prNumber),
		"--repo", repoName,
		"--json", "name,link,bucket",
	)
	out, err := cmd.Output()
	if err != nil {
		// `gh pr checks` exits non-zero when any check has failed (or none
		// exist yet), even though it still prints valid JSON on stdout in the
		// failing-checks case. Try to parse stdout before giving up.
		if len(out) == 0 {
			return nil, err
		}
	}
	var raw []struct {
		Name   string `json:"name"`
		Link   string `json:"link"`
		Bucket string `json:"bucket"`
	}
	if jerr := json.Unmarshal(out, &raw); jerr != nil {
		if err != nil {
			return nil, err
		}
		return nil, jerr
	}
	checks := make([]Check, 0)
	for _, c := range raw {
		if c.Bucket == "fail" || c.Bucket == "cancel" {
			checks = append(checks, Check{Name: c.Name, Link: c.Link, Bucket: c.Bucket})
		}
	}
	return checks, nil
}

// Issue is a GitHub issue as returned by `gh issue list`.
type Issue struct {
	Number int
	Title  string
	Body   string
	URL    string
	Labels []string // label names only
}

// ListOpenIssues returns open issues for the given repo (org/repo format),
// optionally filtered to issues carrying the given label (empty = all open
// issues). Pull requests are never included — `gh issue list` excludes them.
func ListOpenIssues(ctx context.Context, repoName, label string) ([]Issue, error) {
	args := []string{"issue", "list",
		"--repo", repoName,
		"--state", "open",
		"--json", "number,title,body,url,labels",
		"--limit", "200",
	}
	if label != "" {
		args = append(args, "--label", label)
	}
	metrics.GhCallsTotal.WithLabelValues("issue_list").Inc()
	out, err := runGH(ctx, args...).Output()
	if err != nil {
		return nil, err
	}

	var raw []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		URL    string `json:"url"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}

	issues := make([]Issue, 0, len(raw))
	for _, r := range raw {
		labels := make([]string, 0, len(r.Labels))
		for _, l := range r.Labels {
			labels = append(labels, l.Name)
		}
		issues = append(issues, Issue{
			Number: r.Number,
			Title:  r.Title,
			Body:   r.Body,
			URL:    r.URL,
			Labels: labels,
		})
	}
	return issues, nil
}

// AddIssueLabel adds a label to a GitHub issue via the gh CLI. Used by the
// write-back feature to signal "an agent is already working on this" without
// requiring the label to previously exist on the issue (it must already exist
// on the repo, though — `gh issue edit --add-label` fails if the label itself
// hasn't been created in the repo's label set).
func AddIssueLabel(ctx context.Context, repoName string, issueNumber int, label string) error {
	metrics.GhCallsTotal.WithLabelValues("issue_label_add").Inc()
	out, err := runGH(ctx, "issue", "edit", fmt.Sprint(issueNumber),
		"--repo", repoName,
		"--add-label", label,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh issue edit --add-label: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CommentOnIssue posts a comment on a GitHub issue via the gh CLI. Used by the
// write-back feature to link the PR opened for an imported issue.
func CommentOnIssue(ctx context.Context, repoName string, issueNumber int, body string) error {
	metrics.GhCallsTotal.WithLabelValues("issue_comment").Inc()
	out, err := runGH(ctx, "issue", "comment", fmt.Sprint(issueNumber),
		"--repo", repoName,
		"--body", body,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh issue comment: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CloseIssueWithComment closes a GitHub issue and posts a comment in the same
// call via the gh CLI. Used by the write-back feature once a task's PR merges.
func CloseIssueWithComment(ctx context.Context, repoName string, issueNumber int, body string) error {
	metrics.GhCallsTotal.WithLabelValues("issue_close").Inc()
	out, err := runGH(ctx, "issue", "close", fmt.Sprint(issueNumber),
		"--repo", repoName,
		"--comment", body,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh issue close: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ParseGitHubName extracts the "org/repo" name from a GitHub remote URL.
// It handles both HTTPS (https://github.com/org/repo[.git]) and SSH
// (git@github.com:org/repo[.git]) formats.
// Returns ("", false) if the URL is not a recognised GitHub URL.
func ParseGitHubName(remoteURL string) (string, bool) {
	remoteURL = strings.TrimSpace(remoteURL)

	// HTTPS: https://github.com/org/repo or https://github.com/org/repo.git
	if strings.HasPrefix(remoteURL, "https://github.com/") {
		rest := strings.TrimPrefix(remoteURL, "https://github.com/")
		rest = strings.TrimSuffix(rest, ".git")
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return parts[0] + "/" + parts[1], true
		}
	}

	// SSH: git@github.com:org/repo or git@github.com:org/repo.git
	if strings.HasPrefix(remoteURL, "git@github.com:") {
		rest := strings.TrimPrefix(remoteURL, "git@github.com:")
		rest = strings.TrimSuffix(rest, ".git")
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return parts[0] + "/" + parts[1], true
		}
	}

	return "", false
}
