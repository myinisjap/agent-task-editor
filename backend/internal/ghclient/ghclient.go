// Package ghclient is the GitHub implementation of the internal/forge.Forge
// interface. It talks to the GitHub REST API directly over HTTP (like
// forge/gitea) instead of spawning gh CLI subprocesses, so a ghsync sweep or
// issue-import poll costs HTTP requests rather than forked processes.
//
// Authentication reuses what the gh CLI used: GH_TOKEN, then GITHUB_TOKEN,
// then the output of `gh auth token` (so a ~/.config/gh login keeps working).
// GITHUB_API_URL optionally overrides the API base (GitHub Enterprise,
// tests); default https://api.github.com.
package ghclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agentuser"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
)

// GitHub is the GitHub implementation of forge.Forge. It has no state of its
// own — every method delegates to the package-level functions below, which
// call the GitHub REST API (see apiDo). Registered with the forge package's
// selection registry in init() below so forge.ForRemote can pick it for any
// github.com remote.
type GitHub struct{}

var _ forge.Forge = GitHub{}

func init() {
	forge.Register(GitHub{})
}

// Name implements forge.Forge. Matches tasksource.GitHubIssues.Name().
func (GitHub) Name() string { return "github" }

func (GitHub) ParseRepoName(remoteURL string) (string, bool) { return ParseGitHubName(remoteURL) }

func (GitHub) PRForBranch(ctx context.Context, repoName, branch string) (state, prURL string, prNumber int, err error) {
	return GetPRForBranch(ctx, repoName, branch)
}

func (GitHub) CreatePR(ctx context.Context, repoName, branch, base, title, body string) (state, prURL string, err error) {
	return CreatePR(ctx, repoName, branch, base, title, body)
}

func (GitHub) PRHead(ctx context.Context, repoName, branch string) (forge.PRHead, error) {
	return GetPRHead(ctx, repoName, branch)
}

func (GitHub) PRReviews(ctx context.Context, repoName string, prNumber int) ([]forge.Review, error) {
	return GetPRReviews(ctx, repoName, prNumber)
}

func (GitHub) PRReviewComments(ctx context.Context, repoName string, prNumber int) ([]forge.PRReviewComment, error) {
	return GetPRReviewComments(ctx, repoName, prNumber)
}

func (GitHub) FailedChecks(ctx context.Context, repoName string, prNumber int) ([]forge.Check, error) {
	return GetFailedChecks(ctx, repoName, prNumber)
}

func (GitHub) ListOpenIssues(ctx context.Context, repoName, label string) ([]forge.Issue, error) {
	return ListOpenIssues(ctx, repoName, label)
}

func (GitHub) GetIssueComments(ctx context.Context, repoName string, issueNumber int) ([]forge.IssueComment, error) {
	return GetIssueComments(ctx, repoName, issueNumber)
}

func (GitHub) AddIssueLabel(ctx context.Context, repoName string, issueNumber int, label string) error {
	return AddIssueLabel(ctx, repoName, issueNumber, label)
}

func (GitHub) CommentOnIssue(ctx context.Context, repoName string, issueNumber int, body string) error {
	return CommentOnIssue(ctx, repoName, issueNumber, body)
}

func (GitHub) CloseIssueWithComment(ctx context.Context, repoName string, issueNumber int, body string) error {
	return CloseIssueWithComment(ctx, repoName, issueNumber, body)
}

func (GitHub) AuthStatus() (authed bool, note string) { return GHAuthStatus() }

// CompareURL builds a GitHub compare URL for the given repo/base/branch with
// a pre-filled PR title and body, so a human can open a properly-described PR
// in one click without needing GitHub auth.
func (GitHub) CompareURL(repoName, base, branch, title, body string) string {
	q := url.Values{}
	q.Set("expand", "1")
	q.Set("title", title)
	q.Set("body", body)
	return fmt.Sprintf("https://github.com/%s/compare/%s...%s?%s", repoName, base, branch, q.Encode())
}

// httpClient issues every API request. Overridable in tests.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// apiBase returns the REST API base URL (no trailing slash).
func apiBase() string {
	if b := strings.TrimSpace(os.Getenv("GITHUB_API_URL")); b != "" {
		return strings.TrimSuffix(b, "/")
	}
	return "https://api.github.com"
}

var (
	cliTokenMu     sync.Mutex
	cliTokenCached string
	cliTokenAt     time.Time
)

// cliTokenTTL bounds how long a token obtained from `gh auth token` is reused
// before re-running the CLI (so a re-login is eventually picked up while a
// sweep doesn't fork `gh` per request).
const cliTokenTTL = 5 * time.Minute

// ghAuthToken runs `gh auth token`. Overridable in tests.
var ghAuthToken = func() (string, error) {
	out, err := agentuser.ServerCommand("gh", "auth", "token").Output()
	return strings.TrimSpace(string(out)), err
}

// resolveToken returns the API token and a short description of its source.
// Order: GH_TOKEN, GITHUB_TOKEN, `gh auth token` (cached briefly).
func resolveToken() (token, source string) {
	if t := strings.TrimSpace(os.Getenv("GH_TOKEN")); t != "" {
		return t, "GH_TOKEN env var"
	}
	if t := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); t != "" {
		return t, "GITHUB_TOKEN env var"
	}
	cliTokenMu.Lock()
	defer cliTokenMu.Unlock()
	if cliTokenCached != "" && time.Since(cliTokenAt) < cliTokenTTL {
		return cliTokenCached, "gh auth"
	}
	if t, err := ghAuthToken(); err == nil && t != "" {
		cliTokenCached, cliTokenAt = t, time.Now()
		return t, "gh auth"
	}
	cliTokenCached = ""
	return "", ""
}

// GHAuthStatus reports whether a GitHub API token is available (GH_TOKEN,
// GITHUB_TOKEN, or a gh CLI login). It does not make a network call.
func GHAuthStatus() (authed bool, note string) {
	metrics.GhCallsTotal.WithLabelValues("auth_status").Inc()
	if t, src := resolveToken(); t != "" {
		return true, src
	}
	return false, "no GitHub token: set GH_TOKEN or GITHUB_TOKEN, or run `gh auth login`"
}

// apiError is a non-2xx response from the GitHub API.
type apiError struct {
	Status int
	Body   string
	Method string
	Path   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("github %s %s: HTTP %d: %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

func isStatus(err error, code int) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == code
}

// apiDo performs one request against the GitHub REST API. path is relative
// to the API base (leading slash, already escaped, may include a query) or an
// absolute URL (pagination). payload, if non-nil, is JSON-encoded as the
// body. Returns the response body and the rel="next" pagination URL, if any.
func apiDo(ctx context.Context, method, path string, payload any) ([]byte, string, error) {
	full := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		full = apiBase() + path
	}
	var rdr io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, "", err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, rdr)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "agent-task-editor")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok, _ := resolveToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", &apiError{Status: resp.StatusCode, Body: string(body), Method: method, Path: path}
	}
	return body, nextLink(resp.Header.Get("Link")), nil
}

// nextLink extracts the rel="next" URL from a Link header.
func nextLink(h string) string {
	for _, part := range strings.Split(h, ",") {
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		for _, s := range segs[1:] {
			if strings.TrimSpace(s) == `rel="next"` {
				return strings.Trim(strings.TrimSpace(segs[0]), "<>")
			}
		}
	}
	return ""
}

// apiPages GETs path and follows pagination, calling fn with each page body.
func apiPages(ctx context.Context, path string, fn func(page []byte) error) error {
	next := path
	for next != "" {
		body, n, err := apiDo(ctx, http.MethodGet, next, nil)
		if err != nil {
			return err
		}
		if err := fn(body); err != nil {
			return err
		}
		next = n
	}
	return nil
}

// repoPath returns "/repos/owner/repo" with each segment escaped.
func repoPath(repoName string) string {
	return "/repos/" + escapeSegments(repoName)
}

func escapeSegments(s string) string {
	parts := strings.Split(s, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// branchExists reports whether the branch exists on the remote (404 = no).
func branchExists(ctx context.Context, repoName, branch string) (bool, error) {
	metrics.GhCallsTotal.WithLabelValues("branch_check").Inc()
	_, _, err := apiDo(ctx, http.MethodGet, repoPath(repoName)+"/branches/"+escapeSegments(branch), nil)
	if err != nil {
		if isStatus(err, http.StatusNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// rawPR is the subset of a pull request payload we read.
type rawPR struct {
	Number    int    `json:"number"`
	State     string `json:"state"`
	HTMLURL   string `json:"html_url"`
	UpdatedAt string `json:"updated_at"`
	MergedAt  string `json:"merged_at"`
	Mergeable *bool  `json:"mergeable"`
	Head      struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// rawState returns the raw (open/merged/closed) state of the PR.
func (p rawPR) rawState() string {
	if p.MergedAt != "" {
		return "merged"
	}
	return p.State
}

// listPRsForBranch returns the most recent PR (any state) whose head is the
// given branch in the same repo, or nil if none.
func listPRsForBranch(ctx context.Context, repoName, branch string) (*rawPR, error) {
	owner := strings.SplitN(repoName, "/", 2)[0]
	q := url.Values{}
	q.Set("head", owner+":"+branch)
	q.Set("state", "all")
	q.Set("per_page", "1")
	body, _, err := apiDo(ctx, http.MethodGet, repoPath(repoName)+"/pulls?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var prs []rawPR
	if err := json.Unmarshal(body, &prs); err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

func getPR(ctx context.Context, repoName string, number int) (*rawPR, error) {
	body, _, err := apiDo(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", repoPath(repoName), number), nil)
	if err != nil {
		return nil, err
	}
	var pr rawPR
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// GetPRForBranch queries GitHub for a PR matching the given branch in the given
// repo (org/repo format). Returns the normalised state ("pushed", "pr_open",
// "pr_merged", "pr_closed"), the PR web URL, the PR number, and any error.
func GetPRForBranch(ctx context.Context, repoName, branch string) (state, prURL string, prNumber int, err error) {
	metrics.GhCallsTotal.WithLabelValues("pr_list").Inc()
	pr, err := listPRsForBranch(ctx, repoName, branch)
	if err != nil {
		return "", "", 0, err
	}
	if pr == nil {
		ok, err := branchExists(ctx, repoName, branch)
		if err != nil {
			return "", "", 0, err
		}
		if !ok {
			return "", "", 0, nil // branch not on remote yet
		}
		return "pushed", "", 0, nil
	}
	return normalizePRState(pr.rawState()), pr.HTMLURL, pr.Number, nil
}

// normalizePRState maps a raw PR state (open, merged, closed; any case)
// onto the cross-forge normalised state strings ("pr_open", "pr_merged",
// "pr_closed"). Shared by GetPRForBranch and GetPRHead.
func normalizePRState(raw string) string {
	switch strings.ToLower(raw) {
	case "open":
		return "pr_open"
	case "merged":
		return "pr_merged"
	case "closed":
		return "pr_closed"
	default:
		return strings.ToLower(raw)
	}
}

// CreatePR opens a pull request for the given branch, or returns the
// existing PR if one already exists for the branch (idempotent). base is the
// target branch; an empty base uses the repo's default branch. The branch is
// expected to already be pushed.
//
// Returns the normalised state ("pr_open" for a freshly created PR, or the
// existing PR's state) and the PR web URL.
func CreatePR(ctx context.Context, repoName, branch, base, title, body string) (state, prURL string, err error) {
	if s, u, n, gerr := GetPRForBranch(ctx, repoName, branch); gerr == nil && n != 0 {
		return s, u, nil
	}
	if base == "" {
		metrics.GhCallsTotal.WithLabelValues("repo_get").Inc()
		rb, _, rerr := apiDo(ctx, http.MethodGet, repoPath(repoName), nil)
		if rerr != nil {
			return "", "", fmt.Errorf("github create pr: %w", rerr)
		}
		var repo struct {
			DefaultBranch string `json:"default_branch"`
		}
		if jerr := json.Unmarshal(rb, &repo); jerr != nil {
			return "", "", jerr
		}
		base = repo.DefaultBranch
	}
	metrics.GhCallsTotal.WithLabelValues("pr_create").Inc()
	out, _, err := apiDo(ctx, http.MethodPost, repoPath(repoName)+"/pulls", map[string]string{
		"title": title, "head": branch, "base": base, "body": body,
	})
	if err != nil {
		// Race: a PR appeared between our check and create.
		if strings.Contains(err.Error(), "already exists") {
			if s, u, n, gerr := GetPRForBranch(ctx, repoName, branch); gerr == nil && n != 0 {
				return s, u, nil
			}
		}
		return "", "", fmt.Errorf("github create pr: %w", err)
	}
	var pr rawPR
	if err := json.Unmarshal(out, &pr); err != nil {
		return "", "", err
	}
	return "pr_open", pr.HTMLURL, nil
}

// Mergeability is aliased from the forge package so existing ghclient callers
// keep working unchanged while the canonical definitions (and cross-forge
// contract) live in one place. MergeableUnknown, MergeableClean, and
// MergeableConflicting are also aliased. See forge.Mergeability for the full
// doc comment.
type Mergeability = forge.Mergeability

const (
	MergeableUnknown     = forge.MergeableUnknown
	MergeableClean       = forge.MergeableClean
	MergeableConflicting = forge.MergeableConflicting
)

// normalizeMergeable maps the REST API's tri-state mergeable field (true /
// false / null) onto a Mergeability. null (GitHub still computing) maps to
// MergeableUnknown so it is never read as "conflicting".
func normalizeMergeable(m *bool) Mergeability {
	switch {
	case m == nil:
		return MergeableUnknown
	case *m:
		return MergeableClean
	default:
		return MergeableConflicting
	}
}

// PRHead is aliased from forge.PRHead — see its doc comment there.
type PRHead = forge.PRHead

// GetPRHead returns the PR number, head commit SHA, base branch,
// mergeability, normalised state, web URL, and last-updated timestamp for
// the given branch. Returns PRHead{State: "pushed"} if the branch is pushed
// but has no PR yet, or a zero PRHead if the branch doesn't exist on the
// remote. Mergeability requires fetching the single PR (the list endpoint
// omits it), so a PR costs two requests.
func GetPRHead(ctx context.Context, repoName, branch string) (PRHead, error) {
	metrics.GhCallsTotal.WithLabelValues("pr_list_head").Inc()
	pr, err := listPRsForBranch(ctx, repoName, branch)
	if err != nil {
		return PRHead{}, err
	}
	if pr == nil {
		ok, err := branchExists(ctx, repoName, branch)
		if err != nil {
			return PRHead{}, err
		}
		if !ok {
			return PRHead{}, nil
		}
		return PRHead{State: "pushed"}, nil
	}
	full, err := getPR(ctx, repoName, pr.Number)
	if err != nil {
		return PRHead{}, err
	}
	return PRHead{
		Number:    full.Number,
		HeadSHA:   full.Head.SHA,
		BaseRef:   full.Base.Ref,
		Mergeable: normalizeMergeable(full.Mergeable),
		State:     normalizePRState(full.rawState()),
		URL:       full.HTMLURL,
		UpdatedAt: full.UpdatedAt,
	}, nil
}

// Review is aliased from forge.Review — see its doc comment there.
type Review = forge.Review

// GetPRReviews returns all reviews submitted on the given PR, oldest first,
// paginated. Populates Review.AuthorAssociation (see #331).
func GetPRReviews(ctx context.Context, repoName string, prNumber int) ([]Review, error) {
	metrics.GhCallsTotal.WithLabelValues("pr_reviews").Inc()
	reviews := make([]Review, 0)
	err := apiPages(ctx, fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", repoPath(repoName), prNumber), func(b []byte) error {
		var page []struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
			Body  string `json:"body"`
			User  struct {
				Login string `json:"login"`
			} `json:"user"`
			SubmittedAt       string `json:"submitted_at"`
			AuthorAssociation string `json:"author_association"`
		}
		if err := json.Unmarshal(b, &page); err != nil {
			return err
		}
		for _, r := range page {
			reviews = append(reviews, Review{
				ID:                fmt.Sprint(r.ID),
				State:             strings.ToUpper(r.State),
				Body:              r.Body,
				Author:            r.User.Login,
				SubmittedAt:       r.SubmittedAt,
				AuthorAssociation: strings.ToUpper(r.AuthorAssociation),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reviews, nil
}

// PRReviewComment is aliased from forge.PRReviewComment — see its doc
// comment there.
type PRReviewComment = forge.PRReviewComment

// GetPRReviewComments returns all inline review comments left on the given
// PR's diff, across all reviews, paginated. Populates AuthorAssociation (see
// #331).
func GetPRReviewComments(ctx context.Context, repoName string, prNumber int) ([]PRReviewComment, error) {
	metrics.GhCallsTotal.WithLabelValues("pr_review_comments").Inc()
	var comments []PRReviewComment
	err := apiPages(ctx, fmt.Sprintf("%s/pulls/%d/comments?per_page=100", repoPath(repoName), prNumber), func(b []byte) error {
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
			CreatedAt         string `json:"created_at"`
			AuthorAssociation string `json:"author_association"`
		}
		if err := json.Unmarshal(b, &page); err != nil {
			return err
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
				ID:                fmt.Sprint(c.ID),
				Path:              c.Path,
				Line:              line,
				StartLine:         startLine,
				Side:              c.Side,
				Body:              c.Body,
				DiffHunk:          c.DiffHunk,
				CommitID:          c.CommitID,
				Author:            c.User.Login,
				CreatedAt:         c.CreatedAt,
				AuthorAssociation: c.AuthorAssociation,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return comments, nil
}

// Check is aliased from forge.Check — see its doc comment there.
type Check = forge.Check

// GetFailedChecks returns the checks on the given PR's head commit whose
// bucket is "fail" or "cancel" (build/test failures and cancelled runs).
// Pending/skipped/passing checks are excluded. Combines check runs (GitHub
// Actions etc.) and legacy commit statuses, as `gh pr checks` did.
func GetFailedChecks(ctx context.Context, repoName string, prNumber int) ([]Check, error) {
	metrics.GhCallsTotal.WithLabelValues("pr_checks").Inc()
	pr, err := getPR(ctx, repoName, prNumber)
	if err != nil {
		return nil, err
	}
	sha := pr.Head.SHA
	checks := make([]Check, 0)

	err = apiPages(ctx, fmt.Sprintf("%s/commits/%s/check-runs?per_page=100", repoPath(repoName), url.PathEscape(sha)), func(b []byte) error {
		var page struct {
			CheckRuns []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
				HTMLURL    string `json:"html_url"`
			} `json:"check_runs"`
		}
		if err := json.Unmarshal(b, &page); err != nil {
			return err
		}
		for _, c := range page.CheckRuns {
			switch c.Conclusion {
			case "failure", "timed_out", "startup_failure", "action_required":
				checks = append(checks, Check{Name: c.Name, Link: c.HTMLURL, Bucket: "fail"})
			case "cancelled":
				checks = append(checks, Check{Name: c.Name, Link: c.HTMLURL, Bucket: "cancel"})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = apiPages(ctx, fmt.Sprintf("%s/commits/%s/status?per_page=100", repoPath(repoName), url.PathEscape(sha)), func(b []byte) error {
		var page struct {
			Statuses []struct {
				Context   string `json:"context"`
				State     string `json:"state"`
				TargetURL string `json:"target_url"`
			} `json:"statuses"`
		}
		if err := json.Unmarshal(b, &page); err != nil {
			return err
		}
		for _, s := range page.Statuses {
			if s.State == "failure" || s.State == "error" {
				checks = append(checks, Check{Name: s.Context, Link: s.TargetURL, Bucket: "fail"})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return checks, nil
}

// Issue is aliased from forge.Issue — see its doc comment there.
type Issue = forge.Issue

// ListOpenIssues returns every open issue for the given repo (org/repo
// format), optionally filtered to issues carrying the given label (empty =
// all open issues). Pull requests are never included: the REST /issues
// endpoint returns both, so any element with a non-null pull_request field
// is skipped. Follows pagination to the end so the result is never silently
// truncated (see issue #265) — reconciliation of disappeared issues depends
// on the fetch being complete.
func ListOpenIssues(ctx context.Context, repoName, label string) ([]Issue, error) {
	q := url.Values{}
	q.Set("state", "open")
	q.Set("per_page", "100")
	if label != "" {
		q.Set("labels", label)
	}
	metrics.GhCallsTotal.WithLabelValues("issue_list").Inc()
	var issues []Issue
	err := apiPages(ctx, fmt.Sprintf("%s/issues?%s", repoPath(repoName), q.Encode()), func(b []byte) error {
		var page []struct {
			Number            int    `json:"number"`
			Title             string `json:"title"`
			Body              string `json:"body"`
			HTMLURL           string `json:"html_url"`
			AuthorAssociation string `json:"author_association"`
			PullRequest       *struct {
				URL string `json:"url"`
			} `json:"pull_request"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
		}
		if err := json.Unmarshal(b, &page); err != nil {
			return err
		}
		for _, r := range page {
			if r.PullRequest != nil {
				continue
			}
			labels := make([]string, 0, len(r.Labels))
			for _, l := range r.Labels {
				labels = append(labels, l.Name)
			}
			issues = append(issues, Issue{
				Number:            r.Number,
				Title:             r.Title,
				Body:              r.Body,
				URL:               r.HTMLURL,
				Labels:            labels,
				AuthorAssociation: r.AuthorAssociation,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return issues, nil
}

// IssueComment is aliased from forge.IssueComment — see its doc comment there.
type IssueComment = forge.IssueComment

// GetIssueComments returns every comment on the given issue, oldest first.
// Paginates through the full result set.
func GetIssueComments(ctx context.Context, repoName string, issueNumber int) ([]IssueComment, error) {
	metrics.GhCallsTotal.WithLabelValues("issue_comments").Inc()
	var comments []IssueComment
	err := apiPages(ctx, fmt.Sprintf("%s/issues/%d/comments?per_page=100", repoPath(repoName), issueNumber), func(b []byte) error {
		var page []struct {
			ID                int64  `json:"id"`
			Body              string `json:"body"`
			AuthorAssociation string `json:"author_association"`
			User              struct {
				Login string `json:"login"`
			} `json:"user"`
			CreatedAt string `json:"created_at"`
		}
		if err := json.Unmarshal(b, &page); err != nil {
			return err
		}
		for _, c := range page {
			comments = append(comments, IssueComment{
				ID:                fmt.Sprint(c.ID),
				Author:            c.User.Login,
				AuthorAssociation: c.AuthorAssociation,
				Body:              c.Body,
				CreatedAt:         c.CreatedAt,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return comments, nil
}

// AddIssueLabel adds a label to a GitHub issue. Used by the write-back
// feature to signal "an agent is already working on this".
func AddIssueLabel(ctx context.Context, repoName string, issueNumber int, label string) error {
	metrics.GhCallsTotal.WithLabelValues("issue_label_add").Inc()
	_, _, err := apiDo(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/labels", repoPath(repoName), issueNumber),
		map[string][]string{"labels": {label}})
	if err != nil {
		return fmt.Errorf("github add label: %w", err)
	}
	return nil
}

// CommentOnIssue posts a comment on a GitHub issue. Used by the write-back
// feature to link the PR opened for an imported issue.
func CommentOnIssue(ctx context.Context, repoName string, issueNumber int, body string) error {
	metrics.GhCallsTotal.WithLabelValues("issue_comment").Inc()
	_, _, err := apiDo(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", repoPath(repoName), issueNumber),
		map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("github issue comment: %w", err)
	}
	return nil
}

// CloseIssueWithComment posts a comment on a GitHub issue and closes it.
// Used by the write-back feature once a task's PR merges.
func CloseIssueWithComment(ctx context.Context, repoName string, issueNumber int, body string) error {
	metrics.GhCallsTotal.WithLabelValues("issue_close").Inc()
	if _, _, err := apiDo(ctx, http.MethodPost, fmt.Sprintf("%s/issues/%d/comments", repoPath(repoName), issueNumber),
		map[string]string{"body": body}); err != nil {
		return fmt.Errorf("github issue close: %w", err)
	}
	if _, _, err := apiDo(ctx, http.MethodPatch, fmt.Sprintf("%s/issues/%d", repoPath(repoName), issueNumber),
		map[string]string{"state": "closed"}); err != nil {
		return fmt.Errorf("github issue close: %w", err)
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
