// Package ghsync polls GitHub for PR status updates on eligible tasks and
// pushes real-time updates to connected WebSocket clients.
package ghsync

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Publisher is satisfied by *ws.Hub — it sends events to all connected clients.
type Publisher interface {
	Publish(eventType string, payload map[string]any)
}

// Syncer polls all eligible tasks on a fixed interval and refreshes their
// GitHub PR state via the `gh` CLI. Eligible tasks are those that:
//   - have a branch set
//   - are not already in state "pr_merged"
type Syncer struct {
	q        *gen.Queries
	hub      Publisher
	interval time.Duration
}

// New creates a Syncer that polls on the given interval.
func New(db *sql.DB, hub Publisher, interval time.Duration) *Syncer {
	return &Syncer{
		q:        gen.New(db),
		hub:      hub,
		interval: interval,
	}
}

// Run sweeps on the configured interval until ctx is cancelled.
func (s *Syncer) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

// sweep iterates all tasks and refreshes GitHub PR state for eligible ones.
func (s *Syncer) sweep(ctx context.Context) {
	slog.Info("ghsync: sweep start")
	tasks, err := s.q.ListTasks(ctx)
	if err != nil {
		slog.Warn("ghsync: list tasks failed", "err", err)
		return
	}

	// Build a per-repo cache of resolved repo info to avoid repeated DB queries.
	repoCache := map[string]repoInfo{} // repoID -> repoInfo (ghName == "" => not a GitHub repo)

	checked := 0
	for _, task := range tasks {
		// Skip tasks with no branch — nothing to check.
		if task.Branch == "" {
			continue
		}
		// Skip tasks already in the final desired state.
		if task.GitState == "pr_merged" {
			continue
		}
		// Resolve org/repo and local path for this task's repo (cached).
		info, ok := repoCache[task.RepoID]
		if !ok {
			info = s.resolveRepoInfo(ctx, task.RepoID)
			repoCache[task.RepoID] = info
		}
		if info.ghName == "" {
			continue // not a GitHub repo
		}

		checked++
		s.syncTask(ctx, task, info)
	}
	slog.Info("ghsync: sweep done", "total_tasks", len(tasks), "checked", checked)
}

// repoInfo holds the resolved details for a task's repo needed during a sweep.
type repoInfo struct {
	ghName string // "org/repo"; empty means not a GitHub repo
	path   string // local filesystem path to the repo's main clone
}

// resolveRepoInfo fetches the repo from DB and extracts the "org/repo" name
// plus local path. ghName is "" if the repo has no remote URL or is not a
// GitHub URL.
func (s *Syncer) resolveRepoInfo(ctx context.Context, repoID string) repoInfo {
	repo, err := s.q.GetRepo(ctx, repoID)
	if err != nil {
		slog.Warn("ghsync: get repo", "repo_id", repoID, "err", err)
		return repoInfo{}
	}
	if repo.RemoteUrl == nil || *repo.RemoteUrl == "" {
		return repoInfo{}
	}
	name, ok := ghclient.ParseGitHubName(*repo.RemoteUrl)
	if !ok {
		return repoInfo{}
	}
	return repoInfo{ghName: name, path: repo.Path}
}

// syncTask checks the PR state for a single task and updates it if changed.
func (s *Syncer) syncTask(ctx context.Context, task gen.Task, repo repoInfo) {
	state, prURL, _, err := ghclient.GetPRForBranch(ctx, repo.ghName, task.Branch)
	if err != nil {
		slog.Warn("ghsync: get PR for branch", "task_id", task.ID, "branch", task.Branch, "err", err)
		return
	}

	if state == task.GitState {
		return // no change — nothing to do
	}

	if _, err := s.q.UpdateTaskGitState(ctx, gen.UpdateTaskGitStateParams{
		GitState: state,
		ID:       task.ID,
	}); err != nil {
		slog.Warn("ghsync: update git state", "task_id", task.ID, "err", err)
		return
	}

	slog.Info("ghsync: git state updated", "task_id", task.ID, "old_state", task.GitState, "new_state", state)

	s.hub.Publish("task.git_state_changed", map[string]any{
		"task_id":   task.ID,
		"git_state": state,
		"pr_url":    prURL,
	})

	// Once GitHub confirms the PR is merged, the branch's work is preserved
	// upstream and is no longer needed locally — clean it up. Closed-without-
	// merge PRs are left alone so a human can still inspect/reopen the branch.
	if state == "pr_merged" {
		s.cleanupMergedBranch(ctx, task, repo.path)
	}
}

// cleanupMergedBranch removes the task's worktree (if any is still attached)
// and deletes its local branch from the main clone at repoPath. Only the
// local branch is touched — any remote branch (e.g. on origin) is left as-is.
// Best-effort: failures are logged but never propagate, so a cleanup problem
// for one task can't block the sweep or affect other tasks.
func (s *Syncer) cleanupMergedBranch(ctx context.Context, task gen.Task, repoPath string) {
	if task.Branch == "" || repoPath == "" {
		return
	}
	// The worktree is normally already removed by the workflow engine's
	// OnTerminal hook by the time a PR is confirmed merged, but ghsync runs
	// independently of the workflow engine, so don't assume that happened.
	if task.WorktreePath != "" {
		if err := agent.RemoveWorktree(ctx, repoPath, task.WorktreePath); err != nil {
			slog.Warn("ghsync: remove worktree before branch delete", "task_id", task.ID, "err", err)
			// Continue anyway — branch delete below will fail loudly (and be
			// logged) if the worktree is in fact still attached.
		}
	}
	if err := agent.DeleteLocalBranch(ctx, repoPath, task.Branch); err != nil {
		slog.Warn("ghsync: delete local branch", "task_id", task.ID, "branch", task.Branch, "err", err)
		return
	}
	slog.Info("ghsync: deleted local branch after merge", "task_id", task.ID, "branch", task.Branch)
}
