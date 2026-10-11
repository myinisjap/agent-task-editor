package handlers

import (
	"context"
	"github.com/myinisjap/agent-task-editor/backend/internal/agentuser"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	_ "github.com/myinisjap/agent-task-editor/backend/internal/ghclient" // registers the GitHub forge.Forge implementation
	"github.com/myinisjap/agent-task-editor/backend/internal/prflow"
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

	mb, err := agentuser.ServerCommandContext(r.Context(), "git", "-C", gitDir, "merge-base", task.BaseRef, task.Branch).Output()
	base := task.BaseRef
	if err == nil {
		if s := strings.TrimSpace(string(mb)); s != "" {
			base = s
		}
	}

	out, err := agentuser.ServerCommandContext(r.Context(), "git", "-C", gitDir, "diff", base, task.Branch, "--").Output()
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

// PRURL builds a forge compare URL for the task's branch with a pre-filled PR
// title and body, so a human can open a properly-described PR in one click
// without us needing forge auth or a CLI.
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
	f, repoName, ok := forge.ForRemote(*repo.RemoteUrl)
	if !ok {
		Err(w, http.StatusBadRequest, "repo remote is not a supported forge URL")
		return
	}

	// Compare views want branch names, not remote-tracking refs.
	base := strings.TrimPrefix(task.BaseRef, "origin/")

	// Collect commit subjects on the branch (best-effort; empty if it fails).
	gitDir := prflow.GitDir(task, repo)
	commits := prflow.BranchCommits(r.Context(), gitDir, task.BaseRef, task.Branch)

	body := prflow.BuildBody(task, commits)
	prURL := f.CompareURL(repoName, base, task.Branch, task.Title, body)

	JSON(w, http.StatusOK, map[string]any{"url": prURL})
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
	repo, err := h.q.GetRepo(r.Context(), task.RepoID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to locate repo")
		return
	}

	updated, prURL, err := prflow.New(h.q, h.wb).CreateForTask(r.Context(), task, repo)
	if err != nil {
		Err(w, prCreateStatus(err), err.Error())
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"pr_url":    prURL,
		"git_state": updated.GitState,
	})
}

func prCreateStatus(err error) int {
	switch {
	case prflow.IsPrecondition(err):
		return http.StatusBadRequest
	case prflow.IsUpstream(err):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
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
	f, ghName, ok := forge.ForRemote(*repo.RemoteUrl)
	if !ok {
		Err(w, http.StatusBadRequest, "repo remote is not a supported forge URL")
		return
	}

	state, prURL, prNumber, ghErr := f.PRForBranch(r.Context(), ghName, task.Branch)
	if ghErr != nil {
		// Don't fail hard — return what we have stored plus the error detail
		JSON(w, http.StatusOK, map[string]any{
			"git_state":    task.GitState,
			"pr_url":       task.PrUrl,
			"pr_mergeable": task.PrMergeable,
			"error":        ghErr.Error(),
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

	// Refresh the forge's merge-conflict verdict alongside the PR state, so a
	// manual refresh surfaces a conflict without waiting for the next ghsync
	// sweep. Best-effort: an unreachable/erroring forge call here just leaves
	// the stored value in place rather than failing the whole refresh.
	if prNumber != 0 {
		updated = refreshPRMergeable(r.Context(), h.q, updated, f, ghName)
	}

	// Status write-back to the source GitHub issue (opt-in per repo, no-op if
	// the task wasn't imported or the repo doesn't have it enabled).
	if h.wb != nil {
		h.wb.OnPROpened(r.Context(), updated, repo)
		h.wb.OnPRMerged(r.Context(), updated, repo)
	}

	JSON(w, http.StatusOK, map[string]any{
		"git_state":    updated.GitState,
		"pr_url":       updated.PrUrl,
		"pr_mergeable": updated.PrMergeable,
	})
}

// refreshPRMergeable re-reads the forge's mergeability verdict for the task's
// PR and persists it if it changed, returning the (possibly updated) task.
// Any failure leaves the task untouched: this is a nice-to-have alongside the
// PR state, never a reason to fail the caller.
func refreshPRMergeable(ctx context.Context, q *gen.Queries, task gen.Task, f forge.Forge, ghName string) gen.Task {
	head, err := f.PRHead(ctx, ghName, task.Branch)
	if err != nil || head.Mergeable == "" {
		return task
	}
	if string(head.Mergeable) == task.PrMergeable {
		return task
	}
	updated, err := q.SetTaskPRMergeable(ctx, gen.SetTaskPRMergeableParams{
		PrMergeable: string(head.Mergeable),
		ID:          task.ID,
	})
	if err != nil {
		return task
	}
	return updated
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
