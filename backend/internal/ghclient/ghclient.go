// Package ghclient provides shared GitHub CLI helpers used by both the HTTP
// handlers and the background GitHub-sync goroutine.
package ghclient

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
)

// GHAuthStatus returns whether the gh CLI has valid auth credentials.
// Primary: runs `gh auth status` to check for stored credentials (e.g. from the
// ~/.config/gh volume mount).
// Fallback: checks the GITHUB_TOKEN env var (gh picks it up automatically).
func GHAuthStatus() (authed bool, note string) {
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
