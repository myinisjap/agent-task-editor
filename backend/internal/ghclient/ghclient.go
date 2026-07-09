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
	cmd := exec.CommandContext(ctx, "gh", "pr", "list",
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
		chk := exec.CommandContext(ctx, "gh", "api", "repos/"+repoName+"/branches/"+branch, "--silent")
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
	out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
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
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
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
