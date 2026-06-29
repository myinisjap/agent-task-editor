package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

// ghAuthStatus returns whether gh CLI has valid auth.
// Primary: checks GITHUB_TOKEN env var (gh picks it up automatically).
// Fallback: runs `gh auth status` to check for a stored credential.
func ghAuthStatus() (authed bool, note string) {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return true, "GITHUB_TOKEN env var"
	}
	cmd := exec.Command("gh", "auth", "status")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, "gh auth"
	}
	return false, strings.TrimSpace(string(out))
}

// getPRForBranch queries GitHub for a PR matching the given branch in the given
// repo (org/repo format). Returns the normalised state ("pushed", "pr_open",
// "pr_merged", "pr_closed"), the PR web URL, the PR number, and any error.
func getPRForBranch(ctx context.Context, repoName, branch string) (state, prURL string, prNumber int, err error) {
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
		// Branch exists but no PR yet
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

// GitHubAuthStatus reports whether gh CLI auth is available.
// The frontend calls this on load to show a warning if credentials are missing.
func GitHubAuthStatus(w http.ResponseWriter, r *http.Request) {
	authed, note := ghAuthStatus()
	JSON(w, http.StatusOK, map[string]any{
		"authed": authed,
		"note":   note,
	})
}
