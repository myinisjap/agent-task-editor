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
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Publisher is satisfied by *ws.Hub — it sends events to all connected clients.
type Publisher interface {
	Publish(eventType string, payload map[string]any)
}

// Syncer polls all eligible tasks on a fixed interval and refreshes their
// GitHub PR state via the `gh` CLI. Eligible tasks are those that:
//   - have a branch set
//   - are not archived
//   - are not in a terminal PR state ("pr_merged" or "pr_closed")
type Syncer struct {
	q        *gen.Queries
	hub      Publisher
	interval time.Duration

	// getPR resolves the PR state for a branch. Defaults to
	// ghclient.GetPRForBranch; overridable in tests.
	getPR func(ctx context.Context, repoName, branch string) (state, prURL string, prNumber int, err error)
}

// New creates a Syncer that polls on the given interval.
func New(db *sql.DB, hub Publisher, interval time.Duration) *Syncer {
	return &Syncer{
		q:        gen.New(db),
		hub:      hub,
		interval: interval,
		getPR:    ghclient.GetPRForBranch,
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
	start := time.Now()
	defer func() { metrics.GhsyncSweepDurationSeconds.Observe(time.Since(start).Seconds()) }()

	log := slog.With("component", "ghsync")
	log.Info("ghsync: sweep start")
	// Only tasks worth polling: branch-bearing, not archived, and not already in
	// a terminal PR state (pr_merged/pr_closed). Filtering in SQL keeps the number
	// of `gh` calls per sweep bounded by open work rather than the whole table,
	// so tasks that never get a PR (or whose PR closed unmerged) aren't polled
	// forever as the task table grows.
	tasks, err := s.q.ListGhSyncEligibleTasks(ctx)
	if err != nil {
		log.Warn("ghsync: list tasks failed", "err", err)
		return
	}

	// Build a per-repo cache of resolved repo info to avoid repeated DB queries.
	repoCache := map[string]repoInfo{} // repoID -> repoInfo (ghName == "" => not a GitHub repo)

	checked := 0
	for _, task := range tasks {
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
	log.Info("ghsync: sweep done", "total_tasks", len(tasks), "checked", checked)
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
	log := slog.With("component", "ghsync", "repo_id", repoID)
	repo, err := s.q.GetRepo(ctx, repoID)
	if err != nil {
		log.Warn("ghsync: get repo", "err", err)
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
	log := slog.With("component", "ghsync", "task_id", task.ID)
	state, prURL, _, err := s.getPR(ctx, repo.ghName, task.Branch)
	if err != nil {
		log.Warn("ghsync: get PR for branch", "branch", task.Branch, "err", err)
		return
	}

	if state == task.GitState {
		return // no change — nothing to do
	}

	// Persist the new state, and the PR URL when the live query surfaced one.
	// Keep any previously stored URL if it didn't (e.g. state regressed to a
	// plain "pushed" branch), so we never blank out a valid link.
	storeURL := prURL
	if storeURL == "" {
		storeURL = task.PrUrl
	}
	if _, err := s.q.SetTaskPR(ctx, gen.SetTaskPRParams{
		GitState: state,
		PrUrl:    storeURL,
		ID:       task.ID,
	}); err != nil {
		log.Warn("ghsync: update git state", "err", err)
		return
	}

	log.Info("ghsync: git state updated", "old_state", task.GitState, "new_state", state)

	s.hub.Publish("task.git_state_changed", map[string]any{
		"task_id":   task.ID,
		"git_state": state,
		"pr_url":    storeURL,
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
	log := slog.With("component", "ghsync", "task_id", task.ID)
	// The worktree is normally already removed by the workflow engine's
	// OnTerminal hook by the time a PR is confirmed merged, but ghsync runs
	// independently of the workflow engine, so don't assume that happened.
	if task.WorktreePath != "" {
		if err := agent.RemoveWorktree(ctx, repoPath, task.WorktreePath); err != nil {
			log.Warn("ghsync: remove worktree before branch delete", "err", err)
			// Continue anyway — branch delete below will fail loudly (and be
			// logged) if the worktree is in fact still attached.
		}
	}
	if err := agent.DeleteLocalBranch(ctx, repoPath, task.Branch); err != nil {
		log.Warn("ghsync: delete local branch", "branch", task.Branch, "err", err)
		return
	}
	log.Info("ghsync: deleted local branch after merge", "branch", task.Branch)
}
