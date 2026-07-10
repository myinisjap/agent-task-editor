package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Diff returns the task's accumulated changes: the diff of its branch against
// the merge-base with the ref it forked from. Empty diff if not yet provisioned.
func (h *TasksHandler) Diff(w http.ResponseWriter, r *http.Request) {
	task, err := h.q.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	if task.Branch == "" {
		JSON(w, http.StatusOK, map[string]any{"branch": task.Branch, "diff": ""})
		return
	}
	if !isValidGitRef(task.BaseRef) || !isValidGitRef(task.Branch) {
		Err(w, http.StatusBadRequest, "invalid git ref")
		return
	}

	// Prefer the task's worktree, but once a task reaches a terminal label its
	// worktree is torn down (the branch is kept). Fall back to the repo's main
	// clone, which still has the branch ref.
	gitDir := task.WorktreePath
	if gitDir == "" || !dirExists(gitDir) {
		repo, rerr := h.q.GetRepo(r.Context(), task.RepoID)
		if rerr != nil {
			Err(w, http.StatusInternalServerError, "failed to locate repo")
			return
		}
		gitDir = repo.Path
	}

	mb, err := exec.CommandContext(r.Context(), "git", "-C", gitDir, "merge-base", task.BaseRef, task.Branch).Output()
	base := task.BaseRef
	if err == nil {
		if s := strings.TrimSpace(string(mb)); s != "" {
			base = s
		}
	}

	out, err := exec.CommandContext(r.Context(), "git", "-C", gitDir, "diff", base, task.Branch, "--").Output()
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to compute diff")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"branch": task.Branch, "diff": string(out)})
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// PRURL builds a GitHub compare URL for the task's branch with a pre-filled PR
// title and body, so a human can open a properly-described PR in one click
// without us needing GitHub auth or the gh CLI.
func (h *TasksHandler) PRURL(w http.ResponseWriter, r *http.Request) {
	task, err := h.q.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	if task.Branch == "" {
		Err(w, http.StatusBadRequest, "task has no branch yet")
		return
	}
	repo, err := h.q.GetRepo(r.Context(), task.RepoID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to locate repo")
		return
	}
	if repo.RemoteUrl == nil {
		Err(w, http.StatusBadRequest, "repo has no remote_url")
		return
	}
	ghName, ok := ghclient.ParseGitHubName(*repo.RemoteUrl)
	if !ok {
		Err(w, http.StatusBadRequest, "repo remote is not a GitHub URL")
		return
	}

	// GitHub compare wants branch names, not remote-tracking refs.
	base := strings.TrimPrefix(task.BaseRef, "origin/")

	// Collect commit subjects on the branch (best-effort; empty if it fails).
	gitDir := task.WorktreePath
	if gitDir == "" || !dirExists(gitDir) {
		gitDir = repo.Path
	}
	commits := collectBranchCommits(r.Context(), gitDir, task.BaseRef, task.Branch)

	body := buildPRBody(task, commits)
	q := url.Values{}
	q.Set("expand", "1")
	q.Set("title", task.Title)
	q.Set("body", body)
	prURL := fmt.Sprintf("https://github.com/%s/compare/%s...%s?%s", ghName, base, task.Branch, q.Encode())

	JSON(w, http.StatusOK, map[string]any{"url": prURL})
}

// collectBranchCommits returns the commit subjects unique to the task's branch
// relative to its base ref (best-effort — empty slice if the git log fails or
// either ref is invalid).
func collectBranchCommits(ctx context.Context, gitDir, baseRef, branch string) []string {
	if !isValidGitRef(baseRef) || !isValidGitRef(branch) {
		return nil
	}
	out, err := exec.CommandContext(ctx, "git", "-C", gitDir, "log", "--format=%s", baseRef+".."+branch).Output()
	if err != nil {
		return nil
	}
	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			commits = append(commits, line)
		}
	}
	return commits
}

// CreatePR pushes the task's branch to origin and opens a GitHub pull request
// via the gh CLI, then stores the resulting PR URL and git state on the task.
// It is idempotent: if a PR already exists for the branch, that PR is returned
// instead of erroring. Requires the repo to have a GitHub remote, the task to
// have a provisioned branch, and gh to be authenticated.
func (h *TasksHandler) CreatePR(w http.ResponseWriter, r *http.Request) {
	task, err := h.q.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	if task.Branch == "" {
		Err(w, http.StatusBadRequest, "task has no branch yet")
		return
	}
	repo, err := h.q.GetRepo(r.Context(), task.RepoID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to locate repo")
		return
	}
	if repo.RemoteUrl == nil {
		Err(w, http.StatusBadRequest, "repo has no remote_url")
		return
	}
	ghName, ok := ghclient.ParseGitHubName(*repo.RemoteUrl)
	if !ok {
		Err(w, http.StatusBadRequest, "repo remote is not a GitHub URL")
		return
	}

	// Push the branch first. Push from the worktree if it still exists,
	// otherwise from the main clone — the branch ref lives there too once the
	// worktree has been torn down on a terminal transition.
	gitDir := task.WorktreePath
	if gitDir == "" || !dirExists(gitDir) {
		gitDir = repo.Path
	}
	if err := agent.PushBranch(r.Context(), gitDir, task.Branch); err != nil {
		Err(w, http.StatusInternalServerError, "failed to push branch: "+err.Error())
		return
	}

	// gh compare/PR base wants a branch name, not a remote-tracking ref.
	base := strings.TrimPrefix(task.BaseRef, "origin/")
	body := buildPRBody(task, collectBranchCommits(r.Context(), gitDir, task.BaseRef, task.Branch))

	state, prURL, err := ghclient.CreatePR(r.Context(), ghName, task.Branch, base, task.Title, body)
	if err != nil {
		Err(w, http.StatusBadGateway, err.Error())
		return
	}

	updated, err := h.q.SetTaskPR(r.Context(), gen.SetTaskPRParams{
		GitState: state,
		PrUrl:    prURL,
		ID:       task.ID,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Status write-back to the source GitHub issue (opt-in per repo, no-op if
	// the task wasn't imported or the repo doesn't have it enabled).
	if h.wb != nil {
		h.wb.OnPROpened(r.Context(), updated, repo)
		h.wb.OnPRMerged(r.Context(), updated, repo)
	}

	JSON(w, http.StatusOK, map[string]any{
		"pr_url":    prURL,
		"git_state": updated.GitState,
	})
}

// buildPRBody assembles a markdown PR description from the task and its commits.
func buildPRBody(task gen.Task, commits []string) string {
	var b strings.Builder
	if task.Description != "" {
		b.WriteString(task.Description)
		b.WriteString("\n\n")
	}
	if task.AgentNotes != "" {
		b.WriteString("### What changed\n\n")
		b.WriteString(task.AgentNotes)
		b.WriteString("\n\n")
	}
	if len(commits) > 0 {
		b.WriteString("### Commits\n\n")
		for _, c := range commits {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// GitHubStatus fetches live GitHub PR state for the task's branch using the gh
// CLI. It updates the stored git_state and returns the current state plus the
// PR URL (if any).
func (h *TasksHandler) GitHubStatus(w http.ResponseWriter, r *http.Request) {
	task, err := h.q.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	if task.Branch == "" {
		JSON(w, http.StatusOK, map[string]any{
			"git_state": "none",
			"pr_url":    "",
		})
		return
	}
	repo, err := h.q.GetRepo(r.Context(), task.RepoID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "repo not found")
		return
	}
	if repo.RemoteUrl == nil {
		Err(w, http.StatusBadRequest, "repo has no remote_url")
		return
	}
	ghName, ok := ghclient.ParseGitHubName(*repo.RemoteUrl)
	if !ok {
		Err(w, http.StatusBadRequest, "repo remote is not a GitHub URL")
		return
	}

	state, prURL, _, ghErr := ghclient.GetPRForBranch(r.Context(), ghName, task.Branch)
	if ghErr != nil {
		// Don't fail hard — return what we have stored plus the error detail
		JSON(w, http.StatusOK, map[string]any{
			"git_state": task.GitState,
			"pr_url":    task.PrUrl,
			"error":     ghErr.Error(),
		})
		return
	}

	// Persist the refreshed state. Keep any previously stored PR URL if the
	// live query didn't surface one (e.g. a branch that's pushed but has no PR).
	storeURL := prURL
	if storeURL == "" {
		storeURL = task.PrUrl
	}
	updated, err := h.q.SetTaskPR(r.Context(), gen.SetTaskPRParams{
		GitState: state,
		PrUrl:    storeURL,
		ID:       task.ID,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Status write-back to the source GitHub issue (opt-in per repo, no-op if
	// the task wasn't imported or the repo doesn't have it enabled).
	if h.wb != nil {
		h.wb.OnPROpened(r.Context(), updated, repo)
		h.wb.OnPRMerged(r.Context(), updated, repo)
	}

	JSON(w, http.StatusOK, map[string]any{
		"git_state": updated.GitState,
		"pr_url":    updated.PrUrl,
	})
}

// UpdateGitState allows humans or agents to manually set the git state of a task.
func (h *TasksHandler) UpdateGitState(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GitState string `json:"git_state"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	validStates := map[string]bool{
		"": true, "pushed": true, "pr_open": true, "pr_merged": true, "pr_closed": true,
	}
	if !validStates[body.GitState] {
		Err(w, http.StatusBadRequest, "invalid git_state value")
		return
	}
	task, err := h.q.UpdateTaskGitState(r.Context(), gen.UpdateTaskGitStateParams{
		GitState: body.GitState,
		ID:       chi.URLParam(r, "id"),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	// A human manually marking a task pr_merged should still close the source
	// issue if the repo has write-back enabled — treat this the same as the
	// automatic pr_merged detection in ghsync/CreatePR/GitHubStatus.
	if h.wb != nil && body.GitState == "pr_merged" {
		if repo, rerr := h.q.GetRepo(r.Context(), task.RepoID); rerr == nil {
			h.wb.OnPRMerged(r.Context(), task, repo)
		}
	}

	JSON(w, http.StatusOK, toTaskResponse(task))
}
