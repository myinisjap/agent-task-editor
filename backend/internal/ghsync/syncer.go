// Package ghsync polls GitHub for PR status updates on eligible tasks and
// pushes real-time updates to connected WebSocket clients.
package ghsync

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

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
//   - are not in a terminal workflow label
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
	tasks, err := s.q.ListTasks(ctx)
	if err != nil {
		slog.Warn("ghsync: list tasks failed", "err", err)
		return
	}

	// Build a per-workflow cache of label terminal status to avoid repeated DB
	// queries for the same workflow.
	// map[workflowID][labelName] = isTerminal
	terminalCache := map[string]map[string]bool{}

	// Build a per-repo cache of ghName ("org/repo") to avoid repeated DB queries.
	repoCache := map[string]string{} // repoID -> ghName (empty = not a GitHub repo)

	for _, task := range tasks {
		// Skip tasks with no branch — nothing to check.
		if task.Branch == "" {
			continue
		}
		// Skip tasks already in the final desired state.
		if task.GitState == "pr_merged" {
			continue
		}
		// Skip tasks in a terminal label.
		if s.isTerminalLabel(ctx, task.WorkflowID, task.Label, terminalCache) {
			continue
		}

		// Resolve org/repo for this task's repo (cached).
		ghName, ok := repoCache[task.RepoID]
		if !ok {
			ghName = s.resolveGHName(ctx, task.RepoID)
			repoCache[task.RepoID] = ghName
		}
		if ghName == "" {
			continue // not a GitHub repo
		}

		s.syncTask(ctx, task, ghName)
	}
}

// isTerminalLabel returns true if the given label is terminal in the workflow.
func (s *Syncer) isTerminalLabel(ctx context.Context, workflowID, label string, cache map[string]map[string]bool) bool {
	if _, loaded := cache[workflowID]; !loaded {
		labels, err := s.q.ListWorkflowLabels(ctx, workflowID)
		if err != nil {
			slog.Warn("ghsync: list workflow labels", "workflow_id", workflowID, "err", err)
			cache[workflowID] = map[string]bool{} // empty — don't re-query
			return false
		}
		m := make(map[string]bool, len(labels))
		for _, l := range labels {
			m[l.Name] = l.IsTerminal != 0
		}
		cache[workflowID] = m
	}
	return cache[workflowID][label]
}

// resolveGHName fetches the repo from DB and extracts the "org/repo" name.
// Returns "" if the repo has no remote URL or is not a GitHub URL.
func (s *Syncer) resolveGHName(ctx context.Context, repoID string) string {
	repo, err := s.q.GetRepo(ctx, repoID)
	if err != nil {
		slog.Warn("ghsync: get repo", "repo_id", repoID, "err", err)
		return ""
	}
	if repo.RemoteUrl == nil || *repo.RemoteUrl == "" {
		return ""
	}
	name, ok := ghclient.ParseGitHubName(*repo.RemoteUrl)
	if !ok {
		return ""
	}
	return name
}

// syncTask checks the PR state for a single task and updates it if changed.
func (s *Syncer) syncTask(ctx context.Context, task gen.Task, ghName string) {
	state, prURL, _, err := ghclient.GetPRForBranch(ctx, ghName, task.Branch)
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
}
