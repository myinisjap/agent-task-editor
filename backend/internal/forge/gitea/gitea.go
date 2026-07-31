// Package gitea implements internal/forge.Forge against a self-hosted Gitea
// instance's REST API (https://docs.gitea.io/en-us/api-usage/), so
// agent-task-editor's PR-state sync, one-click PR creation, and issue import
// work against Gitea remotes exactly the same way they already do against
// GitHub (see internal/ghclient).
//
// Unlike GitHub (which is reached through the gh CLI), Gitea is talked to
// directly over HTTP using net/http and a personal access token, since there
// is no equivalent widely-installed CLI to shell out to. Authentication is
// configured via environment variables:
//
//   - GITEA_HOST: the host(s) this implementation should claim (comma
//     separated, e.g. "git.example.com,gitea.internal:3000"). Required —
//     with no configured host, ParseRepoName never matches anything, so
//     ForRemote falls through to any other registered forge (or ok=false).
//     This mirrors the self-hosted-instance discovery problem GitHub doesn't
//     have (github.com is a fixed, well-known host).
//   - GITEA_TOKEN: a personal access token with repo read/write scope, sent
//     as an Authorization: token <GITEA_TOKEN> header on every request.
//   - GITEA_BASE_URL: optional override for the API base URL (defaults to
//     "https://<host>" using the first configured host). Set this if the
//     instance is reached over plain HTTP or via a different scheme/port
//     than what appears in git remote URLs (e.g. remotes use an internal
//     SSH-only hostname but the API is exposed elsewhere).
package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
)

// Gitea is the Gitea implementation of forge.Forge. Constructed with New
// (which reads GITEA_HOST/GITEA_TOKEN/GITEA_BASE_URL) and registered with the
// forge package's selection registry in init() below, so forge.ForRemote can
// pick it for any remote whose host matches GITEA_HOST.
type Gitea struct {
	// hosts is the set of git remote hostnames (lowercase, no port unless the
	// configured value included one) this instance claims via ParseRepoName.
	hosts []string
	// baseURL is the API base, e.g. "https://git.example.com" (no trailing
	// slash, no /api/v1 suffix — appended per-request).
	baseURL string
	// token is the personal access token sent on every API request. Empty is
	// valid (unauthenticated/anonymous access to a fully public instance),
	// but AuthStatus reports unauthenticated in that case.
	token string
	// httpClient issues every request. A package-level default with a
	// bounded timeout; overridable in tests.
	httpClient *http.Client
}

var _ forge.Forge = (*Gitea)(nil)

// New builds a Gitea forge.Forge from environment variables. Returns nil, ok
// == false if GITEA_HOST is unset — with no host configured, this
// implementation has nothing to claim and should not be registered at all
// (see init below), since a nil-host Gitea would otherwise silently swallow
// every remote via a Register call it can never actually match).
func New() (*Gitea, bool) {
	rawHosts := strings.TrimSpace(os.Getenv("GITEA_HOST"))
	if rawHosts == "" {
		return nil, false
	}
	var hosts []string
	for _, h := range strings.Split(rawHosts, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) == 0 {
		return nil, false
	}

	base := strings.TrimSpace(os.Getenv("GITEA_BASE_URL"))
	if base == "" {
		base = "https://" + hosts[0]
	}
	base = strings.TrimSuffix(base, "/")

	return &Gitea{
		hosts:   hosts,
		baseURL: base,
		token:   os.Getenv("GITEA_TOKEN"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, true
}

func init() {
	if g, ok := New(); ok {
		forge.Register(g)
	}
}

// Name implements forge.Forge. Matches tasksource.GiteaIssues.Name().
func (g *Gitea) Name() string { return "gitea" }

// ParseRepoName extracts the "owner/repo" name from a Gitea remote URL whose
// host matches one of this instance's configured GITEA_HOST values. Handles
// both HTTPS (https://host[:port]/owner/repo[.git]) and SSH
// (git@host:owner/repo[.git] or ssh://git@host[:port]/owner/repo[.git]) forms.
func (g *Gitea) ParseRepoName(remoteURL string) (string, bool) {
	remoteURL = strings.TrimSpace(remoteURL)

	if rest, ok := stripHTTPSHost(remoteURL, g.hosts); ok {
		return parseOwnerRepo(rest)
	}
	if rest, ok := stripSSHHost(remoteURL, g.hosts); ok {
		return parseOwnerRepo(rest)
	}
	return "", false
}

// stripHTTPSHost strips "https://host[:port]/" for whichever configured host
// matches, returning the remainder of the path.
func stripHTTPSHost(remoteURL string, hosts []string) (string, bool) {
	for _, h := range hosts {
		prefix := "https://" + h + "/"
		if strings.HasPrefix(strings.ToLower(remoteURL), prefix) {
			return remoteURL[len(prefix):], true
		}
	}
	return "", false
}

// stripSSHHost strips the "git@host:" (scp-like) or "ssh://git@host[:port]/"
// forms for whichever configured host matches.
func stripSSHHost(remoteURL string, hosts []string) (string, bool) {
	lower := strings.ToLower(remoteURL)
	for _, h := range hosts {
		if p := "git@" + h + ":"; strings.HasPrefix(lower, p) {
			return remoteURL[len(p):], true
		}
		if p := "ssh://git@" + h + "/"; strings.HasPrefix(lower, p) {
			return remoteURL[len(p):], true
		}
		// ssh://git@host:port/owner/repo
		if p := "ssh://git@" + h + ":"; strings.HasPrefix(lower, p) {
			rest := remoteURL[len(p):]
			if idx := strings.Index(rest, "/"); idx >= 0 {
				return rest[idx+1:], true
			}
		}
	}
	return "", false
}

func parseOwnerRepo(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, ".git")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
		return parts[0] + "/" + parts[1], true
	}
	return "", false
}

// apiCall issues an HTTP request against this instance's API and decodes a
// JSON response body into out (if non-nil). method/path/body follow
// net/http conventions; path is relative to baseURL + "/api/v1" (e.g.
// "/repos/owner/repo/pulls"). Returns the raw response body alongside any
// error so callers that need to tolerate a non-2xx-but-still-useful body
// (mirroring ghclient's `gh pr checks` handling) can inspect it.
func (g *Gitea) apiCall(ctx context.Context, method, path string, body any, out any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+"/api/v1"+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if g.token != "" {
		req.Header.Set("Authorization", "token "+g.token)
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitea api %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return respBody, fmt.Errorf("gitea api %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return respBody, fmt.Errorf("decode response body: %w", err)
		}
	}
	return respBody, nil
}

// giteaPR mirrors the subset of Gitea's PullRequest API object this
// implementation needs. See
// https://gitea.com/api/swagger#/repository/repoListPullRequests.
type giteaPR struct {
	Number  int    `json:"number"`
	State   string `json:"state"` // "open" or "closed"
	Merged  bool   `json:"merged"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		Ref string `json:"ref"`
		Sha string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	// Mergeable is a *bool in Gitea's API: null while the test-merge hasn't
	// been computed yet, true/false once it has. A nil pointer here maps to
	// forge.MergeableUnknown (see normalizeMergeable) — the safe default.
	Mergeable *bool `json:"mergeable"`
}

// normalizeState maps Gitea's PR state (open/closed + a separate merged
// flag) onto the cross-forge normalised state strings ("pushed", "pr_open",
// "pr_merged", "pr_closed").
func normalizeState(pr giteaPR) string {
	if pr.Merged {
		return "pr_merged"
	}
	switch strings.ToLower(pr.State) {
	case "open":
		return "pr_open"
	case "closed":
		return "pr_closed"
	default:
		return "pr_open"
	}
}

// normalizeMergeable maps Gitea's nullable mergeable bool onto a
// forge.Mergeability, defaulting to MergeableUnknown for the nil (not yet
// computed) case rather than guessing — see forge.Mergeability's doc comment.
func normalizeMergeable(m *bool) forge.Mergeability {
	if m == nil {
		return forge.MergeableUnknown
	}
	if *m {
		return forge.MergeableClean
	}
	return forge.MergeableConflicting
}

// findPRForBranch lists PRs on repoName and returns the one whose head ref
// matches branch (Gitea's list-PRs endpoint has no head-branch filter, unlike
// GitHub's, so this fetches every open+closed PR and filters client-side;
// bounded by limit/page since a repo's total PR history could otherwise grow
// unbounded — recent activity is what matters for this lookup).
func (g *Gitea) findPRForBranch(ctx context.Context, repoName, branch string) (giteaPR, bool, error) {
	q := url.Values{}
	q.Set("state", "all")
	q.Set("limit", "50")
	q.Set("sort", "recentupdate")
	path := fmt.Sprintf("/repos/%s/pulls?%s", repoName, q.Encode())

	metrics.GiteaCallsTotal.WithLabelValues("pr_list").Inc()
	var prs []giteaPR
	if _, err := g.apiCall(ctx, http.MethodGet, path, nil, &prs); err != nil {
		return giteaPR{}, false, err
	}
	for _, pr := range prs {
		if pr.Head.Ref == branch {
			return pr, true, nil
		}
	}
	return giteaPR{}, false, nil
}

// branchExists checks whether branch exists on repoName's default remote,
// used to distinguish "pushed but no PR yet" from "branch not pushed" the
// same way ghclient.GetPRForBranch does for GitHub.
func (g *Gitea) branchExists(ctx context.Context, repoName, branch string) bool {
	metrics.GiteaCallsTotal.WithLabelValues("branch_check").Inc()
	_, err := g.apiCall(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/branches/%s", repoName, url.PathEscape(branch)), nil, nil)
	return err == nil
}

// PRForBranch implements forge.Forge.
func (g *Gitea) PRForBranch(ctx context.Context, repoName, branch string) (state, prURL string, prNumber int, err error) {
	pr, found, err := g.findPRForBranch(ctx, repoName, branch)
	if err != nil {
		return "", "", 0, err
	}
	if !found {
		if !g.branchExists(ctx, repoName, branch) {
			return "", "", 0, nil // branch not on remote yet
		}
		return "pushed", "", 0, nil
	}
	return normalizeState(pr), pr.HTMLURL, pr.Number, nil
}

// CreatePR implements forge.Forge. Idempotent: returns the existing PR for
// branch rather than erroring if one is already open, mirroring
// ghclient.CreatePR's behavior.
func (g *Gitea) CreatePR(ctx context.Context, repoName, branch, base, title, body string) (state, prURL string, err error) {
	if pr, found, ferr := g.findPRForBranch(ctx, repoName, branch); ferr == nil && found {
		return normalizeState(pr), pr.HTMLURL, nil
	}

	if base == "" {
		repoInfo := struct {
			DefaultBranch string `json:"default_branch"`
		}{}
		if _, rerr := g.apiCall(ctx, http.MethodGet, "/repos/"+repoName, nil, &repoInfo); rerr == nil {
			base = repoInfo.DefaultBranch
		}
	}

	reqBody := struct {
		Head  string `json:"head"`
		Base  string `json:"base"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}{Head: branch, Base: base, Title: title, Body: body}

	metrics.GiteaCallsTotal.WithLabelValues("pr_create").Inc()
	var pr giteaPR
	if _, err := g.apiCall(ctx, http.MethodPost, "/repos/"+repoName+"/pulls", reqBody, &pr); err != nil {
		// Race: a PR appeared between our check and create.
		if pr2, found, ferr := g.findPRForBranch(ctx, repoName, branch); ferr == nil && found {
			return normalizeState(pr2), pr2.HTMLURL, nil
		}
		return "", "", fmt.Errorf("gitea create pr: %w", err)
	}
	return normalizeState(pr), pr.HTMLURL, nil
}

// PRHead implements forge.Forge.
func (g *Gitea) PRHead(ctx context.Context, repoName, branch string) (forge.PRHead, error) {
	metrics.GiteaCallsTotal.WithLabelValues("pr_list_head").Inc()
	pr, found, err := g.findPRForBranch(ctx, repoName, branch)
	if err != nil {
		return forge.PRHead{}, err
	}
	if !found {
		return forge.PRHead{}, nil
	}
	return forge.PRHead{
		Number:    pr.Number,
		HeadSHA:   pr.Head.Sha,
		BaseRef:   pr.Base.Ref,
		Mergeable: normalizeMergeable(pr.Mergeable),
	}, nil
}

// giteaReview mirrors Gitea's PullReview API object.
// https://gitea.com/api/swagger#/repository/repoListPullReviews
type giteaReview struct {
	ID        int64  `json:"id"`
	State     string `json:"state"` // "APPROVED", "PENDING", "COMMENT", "REQUEST_CHANGES", ...
	Body      string `json:"body"`
	Submitted string `json:"submitted_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// PRReviews implements forge.Forge. Gitea's REQUEST_CHANGES review state
// maps onto GitHub's CHANGES_REQUESTED so ghsync's ingestReviews (which
// filters on that exact string) works unmodified across both forges.
func (g *Gitea) PRReviews(ctx context.Context, repoName string, prNumber int) ([]forge.Review, error) {
	metrics.GiteaCallsTotal.WithLabelValues("pr_reviews").Inc()
	var raw []giteaReview
	if _, err := g.apiCall(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d/reviews", repoName, prNumber), nil, &raw); err != nil {
		return nil, err
	}
	reviews := make([]forge.Review, 0, len(raw))
	for _, r := range raw {
		state := strings.ToUpper(r.State)
		if state == "REQUEST_CHANGES" {
			state = "CHANGES_REQUESTED"
		}
		reviews = append(reviews, forge.Review{
			ID:          strconv.FormatInt(r.ID, 10),
			State:       state,
			Body:        r.Body,
			Author:      r.User.Login,
			SubmittedAt: r.Submitted,
		})
	}
	return reviews, nil
}

// giteaReviewComment mirrors Gitea's PullReviewComment API object.
type giteaReviewComment struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	LineNum   int    `json:"line"` // signed: positive = new/right side, negative = old/left side, per Gitea's convention
	DiffHunk  string `json:"diff_hunk"`
	CommitID  string `json:"commit_id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// PRReviewComments implements forge.Forge.
func (g *Gitea) PRReviewComments(ctx context.Context, repoName string, prNumber int) ([]forge.PRReviewComment, error) {
	metrics.GiteaCallsTotal.WithLabelValues("pr_review_comments").Inc()
	var raw []giteaReviewComment
	if _, err := g.apiCall(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d/comments", repoName, prNumber), nil, &raw); err != nil {
		return nil, err
	}
	comments := make([]forge.PRReviewComment, 0, len(raw))
	for _, c := range raw {
		side := "RIGHT"
		line := c.LineNum
		if line < 0 {
			side = "LEFT"
			line = -line
		}
		comments = append(comments, forge.PRReviewComment{
			ID:        strconv.FormatInt(c.ID, 10),
			Path:      c.Path,
			Line:      line,
			StartLine: line,
			Side:      side,
			Body:      c.Body,
			DiffHunk:  c.DiffHunk,
			CommitID:  c.CommitID,
			Author:    c.User.Login,
			CreatedAt: c.CreatedAt,
		})
	}
	return comments, nil
}

// giteaCheckRun mirrors the subset of Gitea's commit-status API this
// implementation needs (Gitea has no separate "Actions checks" listing API
// as rich as GitHub's `gh pr checks`; commit statuses are the closest
// equivalent and are what Gitea Actions itself reports through).
type giteaCommitStatus struct {
	State       string `json:"status"` // "success", "failure", "error", "pending", "warning"
	Description string `json:"description"`
	TargetURL   string `json:"target_url"`
	Context     string `json:"context"`
}

// FailedChecks implements forge.Forge. Reports commit statuses in a failed
// or error state for the PR's head commit; "pending"/"success"/"warning" are
// excluded, mirroring ghclient.GetFailedChecks's fail/cancel bucket filter.
func (g *Gitea) FailedChecks(ctx context.Context, repoName string, prNumber int) ([]forge.Check, error) {
	pr, found, err := g.prByNumber(ctx, repoName, prNumber)
	if err != nil {
		return nil, err
	}
	if !found || pr.Head.Sha == "" {
		return nil, nil
	}

	metrics.GiteaCallsTotal.WithLabelValues("pr_checks").Inc()
	var statuses []giteaCommitStatus
	if _, err := g.apiCall(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/commits/%s/statuses", repoName, pr.Head.Sha), nil, &statuses); err != nil {
		return nil, err
	}

	checks := make([]forge.Check, 0)
	for _, s := range statuses {
		bucket := ""
		switch strings.ToLower(s.State) {
		case "failure", "error":
			bucket = "fail"
		default:
			continue
		}
		name := s.Context
		if name == "" {
			name = "check"
		}
		checks = append(checks, forge.Check{Name: name, Link: s.TargetURL, Bucket: bucket})
	}
	return checks, nil
}

// prByNumber fetches a single PR by number, used by FailedChecks to resolve
// the head SHA to query commit statuses for.
func (g *Gitea) prByNumber(ctx context.Context, repoName string, prNumber int) (giteaPR, bool, error) {
	var pr giteaPR
	if _, err := g.apiCall(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repoName, prNumber), nil, &pr); err != nil {
		return giteaPR{}, false, err
	}
	return pr, true, nil
}

// giteaIssue mirrors the subset of Gitea's Issue API object this
// implementation needs. Gitea's /issues endpoint (like GitHub's) returns both
// issues and PRs; a PullRequest field set (non-nil) marks the latter.
type giteaIssue struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// ListOpenIssues implements forge.Forge. Paginates through the complete
// result set (never capped) per the Forge contract — see issue #265.
func (g *Gitea) ListOpenIssues(ctx context.Context, repoName, label string) ([]forge.Issue, error) {
	const pageSize = 50
	var issues []forge.Issue

	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("state", "open")
		q.Set("type", "issues") // exclude PRs directly — Gitea supports this filter, unlike GitHub's REST /issues
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("page", strconv.Itoa(page))
		if label != "" {
			q.Set("labels", label)
		}

		metrics.GiteaCallsTotal.WithLabelValues("issue_list").Inc()
		var raw []giteaIssue
		if _, err := g.apiCall(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues?%s", repoName, q.Encode()), nil, &raw); err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			break
		}
		for _, r := range raw {
			if r.PullRequest != nil {
				continue // belt-and-suspenders: type=issues should already exclude these
			}
			labels := make([]string, 0, len(r.Labels))
			for _, l := range r.Labels {
				labels = append(labels, l.Name)
			}
			issues = append(issues, forge.Issue{
				Number: r.Number,
				Title:  r.Title,
				Body:   r.Body,
				URL:    r.HTMLURL,
				Labels: labels,
			})
		}
		if len(raw) < pageSize {
			break
		}
	}
	return issues, nil
}

// giteaComment mirrors Gitea's Comment API object.
type giteaComment struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// GetIssueComments implements forge.Forge. Paginates through the complete
// result set (never capped) per the Forge contract — see issue #265.
//
// Gitea's comment API doesn't return an author_association-equivalent field
// the way GitHub's does, so trust classification (used by
// tasksource.mapIssueComments) instead checks collaborator status per
// author via a follow-up API call, memoized per call to avoid one request
// per comment for repeat commenters.
func (g *Gitea) GetIssueComments(ctx context.Context, repoName string, issueNumber int) ([]forge.IssueComment, error) {
	const pageSize = 50
	var comments []forge.IssueComment
	collaboratorCache := map[string]bool{}

	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("page", strconv.Itoa(page))

		metrics.GiteaCallsTotal.WithLabelValues("issue_comments").Inc()
		var raw []giteaComment
		if _, err := g.apiCall(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d/comments?%s", repoName, issueNumber, q.Encode()), nil, &raw); err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			break
		}
		for _, c := range raw {
			association := "NONE"
			if trusted, ok := collaboratorCache[c.User.Login]; ok {
				if trusted {
					association = "COLLABORATOR"
				}
			} else {
				trusted := g.isCollaborator(ctx, repoName, c.User.Login)
				collaboratorCache[c.User.Login] = trusted
				if trusted {
					association = "COLLABORATOR"
				}
			}
			comments = append(comments, forge.IssueComment{
				ID:                strconv.FormatInt(c.ID, 10),
				Author:            c.User.Login,
				AuthorAssociation: association,
				Body:              c.Body,
				CreatedAt:         c.CreatedAt,
			})
		}
		if len(raw) < pageSize {
			break
		}
	}
	return comments, nil
}

// isCollaborator reports whether username has collaborator access to
// repoName, via Gitea's dedicated collaborator-check endpoint (returns 204
// if true, 404 if false). Best-effort: any error is treated as "not a
// collaborator" (the safer default for trust classification — see
// tasksource.mapIssueComments, which only ingests trusted-author comments).
func (g *Gitea) isCollaborator(ctx context.Context, repoName, username string) bool {
	if username == "" {
		return false
	}
	metrics.GiteaCallsTotal.WithLabelValues("collaborator_check").Inc()
	_, err := g.apiCall(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/collaborators/%s", repoName, url.PathEscape(username)), nil, nil)
	return err == nil
}

// AddIssueLabel implements forge.Forge.
func (g *Gitea) AddIssueLabel(ctx context.Context, repoName string, issueNumber int, label string) error {
	labelID, err := g.resolveLabelID(ctx, repoName, label)
	if err != nil {
		return err
	}
	metrics.GiteaCallsTotal.WithLabelValues("issue_label_add").Inc()
	reqBody := struct {
		Labels []int64 `json:"labels"`
	}{Labels: []int64{labelID}}
	_, err = g.apiCall(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", repoName, issueNumber), reqBody, nil)
	if err != nil {
		return fmt.Errorf("gitea add issue label: %w", err)
	}
	return nil
}

// resolveLabelID looks up a repo label's numeric ID by name, since Gitea's
// label-assignment endpoint takes IDs rather than names. Mirrors the gh CLI
// requiring the label to already exist on the repo's label set.
func (g *Gitea) resolveLabelID(ctx context.Context, repoName, label string) (int64, error) {
	var labels []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if _, err := g.apiCall(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/labels", repoName), nil, &labels); err != nil {
		return 0, fmt.Errorf("gitea list labels: %w", err)
	}
	for _, l := range labels {
		if l.Name == label {
			return l.ID, nil
		}
	}
	return 0, fmt.Errorf("gitea: label %q does not exist on repo %s", label, repoName)
}

// CommentOnIssue implements forge.Forge.
func (g *Gitea) CommentOnIssue(ctx context.Context, repoName string, issueNumber int, body string) error {
	metrics.GiteaCallsTotal.WithLabelValues("issue_comment").Inc()
	reqBody := struct {
		Body string `json:"body"`
	}{Body: body}
	if _, err := g.apiCall(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repoName, issueNumber), reqBody, nil); err != nil {
		return fmt.Errorf("gitea comment on issue: %w", err)
	}
	return nil
}

// CloseIssueWithComment implements forge.Forge. Gitea's API doesn't support
// closing and commenting atomically in one call (unlike `gh issue close
// --comment`), so this posts the comment first, then closes — this ordering
// means a crash between the two calls leaves the comment posted but the
// issue still open (rather than closed with the explanation lost), which is
// the safer partial-failure mode.
func (g *Gitea) CloseIssueWithComment(ctx context.Context, repoName string, issueNumber int, body string) error {
	if err := g.CommentOnIssue(ctx, repoName, issueNumber, body); err != nil {
		return err
	}
	metrics.GiteaCallsTotal.WithLabelValues("issue_close").Inc()
	reqBody := struct {
		State string `json:"state"`
	}{State: "closed"}
	if _, err := g.apiCall(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/%d", repoName, issueNumber), reqBody, nil); err != nil {
		return fmt.Errorf("gitea close issue: %w", err)
	}
	return nil
}

// AuthStatus implements forge.Forge, via Gitea's authenticated-user endpoint.
func (g *Gitea) AuthStatus() (authed bool, note string) {
	if g.token == "" {
		return false, "GITEA_TOKEN not set"
	}
	metrics.GiteaCallsTotal.WithLabelValues("auth_status").Inc()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := g.apiCall(ctx, http.MethodGet, "/user", nil, nil); err != nil {
		return false, err.Error()
	}
	return true, "GITEA_TOKEN env var"
}

// CompareURL implements forge.Forge, building Gitea's compare/pull-request
// web page (https://<host>/<owner>/<repo>/compare/<base>...<branch>), which
// (like GitHub's) accepts pre-filled title/body via query params without
// requiring API auth.
func (g *Gitea) CompareURL(repoName, base, branch, title, body string) string {
	q := url.Values{}
	q.Set("title", title)
	q.Set("body", body)
	return fmt.Sprintf("%s/%s/compare/%s...%s?%s", g.baseURL, repoName, base, branch, q.Encode())
}
