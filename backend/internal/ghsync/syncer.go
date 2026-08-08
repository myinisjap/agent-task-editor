// Package ghsync polls each task's forge (GitHub, or a self-hosted Gitea —
// see internal/forge) for PR/MR status updates on eligible tasks and pushes
// real-time updates to connected WebSocket clients.
package ghsync

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/writeback"
)

// Publisher is satisfied by *ws.Hub — it sends events to all connected clients.
type Publisher interface {
	Publish(eventType string, payload map[string]any)
}

// Syncer polls all eligible tasks on a fixed interval and refreshes their
// PR/MR state against whichever forge (GitHub, a self-hosted Gitea, ...)
// each repo's remote resolves to. Eligible tasks are those that:
//   - have a branch set
//   - are not archived
//   - are not in a terminal PR state ("pr_merged" or "pr_closed")
type Syncer struct {
	q        *gen.Queries
	hub      Publisher
	interval time.Duration
	wb       *writeback.Writeback
	// engine drives the optional auto-transition-on-feedback behavior (see
	// pr_review.go's autoTransitionOnFeedback). Nil disables auto-transition
	// entirely, which keeps tests and non-transition setups simple — mirrors
	// how wb being nil disables write-back.
	engine *workflow.Engine

	// getPRHead resolves the PR head (number, head SHA, base ref,
	// mergeability, normalised state, URL, and last-updated timestamp) for a
	// branch — the single per-task forge call each sweep makes, backing both
	// PR-state sync (syncTask) and PR review/GHA-status feedback ingestion
	// (see pr_review.go). Prior to #340 this was two near-identical forge
	// calls (getPR + getPRHead); folding them into one call per task per
	// sweep is what keeps ghsync's API usage bounded. In production (see
	// New), this is a thin dispatcher that calls PRHead on whichever
	// forge.Forge repoInfo.forge was resolved to (see resolveRepoInfo) —
	// GitHub via ghclient, a self-hosted Gitea via internal/forge/gitea, or
	// any other forge.Forge registered in the future. Kept as an overridable
	// field (rather than calling repo.forge inline at every call site) so
	// existing tests can keep injecting a fake without needing a real
	// forge.Forge or DB-backed repo row.
	//
	// getReviews, getReviewComments, getFailedChecks back the rest of PR
	// review/GHA-status feedback ingestion (see pr_review.go). Default to
	// dispatching to repoInfo.forge the same way getPRHead does; overridable
	// in tests.
	getPRHead         func(ctx context.Context, repo repoInfo, branch string) (forge.PRHead, error)
	getReviews        func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Review, error)
	getReviewComments func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.PRReviewComment, error)
	getFailedChecks   func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Check, error)

	// backoff tracks, per repo, consecutive forge-call failures observed
	// during sweeps, so a rate-limited or otherwise-failing repo's effective
	// poll interval lengthens instead of hammering the forge identically
	// every sweep (see backoff.go). Guarded by backoffMu. nil-safe: every
	// accessor tolerates a nil map, since tests construct Syncer literals
	// directly, bypassing New.
	backoffMu sync.Mutex
	backoff   map[string]*repoBackoff
}

// New creates a Syncer that polls on the given interval. engine may be nil,
// which disables the optional auto-transition-on-PR-feedback behavior (per-
// repo opt-in via pr_review_auto_transition_enabled) while still ingesting
// and surfacing PR review/GHA feedback.
//
// Unlike an earlier version of this Syncer (which always called
// ghclient.GitHub directly, regardless of which forge actually owned a
// repo's remote), the getPRHead/getReviews/getReviewComments/
// getFailedChecks funcs wired here dispatch to repoInfo.forge — the
// forge.Forge resolveRepoInfo already resolves per repo via forge.ForRemote
// — so PR-state sync, review/comment ingestion, and failed-check ingestion
// all work against whichever forge.Forge implementation actually recognises
// a given repo's remote, not just GitHub.
func New(db *sql.DB, hub Publisher, interval time.Duration, engine *workflow.Engine) *Syncer {
	return &Syncer{
		q:        gen.New(db),
		hub:      hub,
		interval: interval,
		wb:       writeback.New(gen.New(db)),
		engine:   engine,
		backoff:  map[string]*repoBackoff{},
		getPRHead: func(ctx context.Context, repo repoInfo, branch string) (forge.PRHead, error) {
			return repo.forge.PRHead(ctx, repo.ghName, branch)
		},
		getReviews: func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Review, error) {
			return repo.forge.PRReviews(ctx, repo.ghName, prNumber)
		},
		getReviewComments: func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.PRReviewComment, error) {
			return repo.forge.PRReviewComments(ctx, repo.ghName, prNumber)
		},
		getFailedChecks: func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Check, error) {
			return repo.forge.FailedChecks(ctx, repo.ghName, prNumber)
		},
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

// sweep iterates all tasks and refreshes PR/MR state for eligible ones,
// against whichever forge (GitHub, a self-hosted Gitea, ...) each task's repo
// resolves to.
func (s *Syncer) sweep(ctx context.Context) {
	start := time.Now()
	defer func() { metrics.GhsyncSweepDurationSeconds.Observe(time.Since(start).Seconds()) }()

	log := slog.With("component", "ghsync")
	log.Info("ghsync: sweep start")
	// Only tasks worth polling: branch-bearing, not archived, and not already in
	// a terminal PR state (pr_merged/pr_closed). Filtering in SQL keeps the number
	// of forge API calls per sweep bounded by open work rather than the whole
	// table, so tasks that never get a PR (or whose PR closed unmerged) aren't
	// polled forever as the task table grows.
	tasks, err := s.q.ListGhSyncEligibleTasks(ctx)
	if err != nil {
		log.Warn("ghsync: list tasks failed", "err", err)
		return
	}

	// Build a per-repo cache of resolved repo info to avoid repeated DB queries.
	repoCache := map[string]repoInfo{} // repoID -> repoInfo (ghName == "" => no registered forge recognises this repo's remote)

	now := time.Now()
	// recordedThisSweep dedupes backoff bookkeeping to at most one
	// success/failure recorded per repo per sweep, no matter how many of the
	// repo's tasks are visited — otherwise a repo with N eligible tasks would
	// rack up N failures (and hit the backoff cap) from a single bad sweep.
	// Sweep-scoped on purpose: it must not survive past this one sweep call.
	recordedThisSweep := map[string]bool{}
	backedOffRepos := map[string]bool{}

	checked := 0
	skippedBackoff := 0
	for _, task := range tasks {
		// Resolve the repo's forge name/implementation and local path (cached).
		info, ok := repoCache[task.RepoID]
		if !ok {
			info = s.resolveRepoInfo(ctx, task.RepoID)
			repoCache[task.RepoID] = info
		}
		if info.ghName == "" {
			continue // no registered forge recognises this repo's remote
		}

		if s.backoffActive(task.RepoID, now) {
			skippedBackoff++
			if !backedOffRepos[task.RepoID] {
				backedOffRepos[task.RepoID] = true
				failures, remaining := s.backoffStatus(task.RepoID, now)
				log.Info("ghsync: repo in error backoff, skipping this sweep",
					"repo_id", task.RepoID, "consecutive_failures", failures, "retry_in", remaining)
			}
			continue
		}

		checked++
		ok = s.syncTask(ctx, task, info)
		if !recordedThisSweep[task.RepoID] {
			recordedThisSweep[task.RepoID] = true
			if ok {
				s.recordForgeSuccess(task.RepoID)
			} else {
				s.recordForgeFailure(task.RepoID, now)
			}
		}
	}

	metrics.GhsyncReposBackedOff.Set(float64(len(backedOffRepos)))
	log.Info("ghsync: sweep done", "total_tasks", len(tasks), "checked", checked, "skipped_backoff", skippedBackoff)
}

// repoInfo holds the resolved details for a task's repo needed during a sweep.
type repoInfo struct {
	ghName string   // canonical repo name (e.g. "org/repo"); empty means no supported forge recognised the remote
	path   string   // local filesystem path to the repo's main clone
	repo   gen.Repo // full repo row, needed by the writeback hooks (e.g. IssueWritebackEnabled)
	// forge is the forge.Forge implementation that recognised this repo's
	// remote URL (resolved once per repo per sweep via forge.ForRemote),
	// used by the getPR/getPRHead/getReviews/getReviewComments/
	// getFailedChecks dispatch funcs wired up in New. Nil in repoInfo zero
	// values and in tests that construct a repoInfo by hand without setting
	// it (those tests instead inject fixed getPR/etc. closures that ignore
	// this field entirely).
	forge forge.Forge
}

// resolveRepoInfo fetches the repo from DB and extracts its forge-name and
// forge.Forge implementation (via forge.ForRemote) plus local path. ghName
// is "" if the repo has no remote URL or no registered forge recognises it.
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
	f, name, ok := forge.ForRemote(*repo.RemoteUrl)
	if !ok {
		return repoInfo{}
	}
	return repoInfo{ghName: name, path: repo.Path, repo: repo, forge: f}
}

// syncTask checks the PR state for a single task and updates it if changed.
// It also, independently of whether the state changed, ingests any new PR
// review feedback / failed GHA checks / base-branch merge conflicts for tasks
// with an open PR (see ingestPRFeedback in pr_review.go) — a task can sit on
// "pr_open" across many sweeps while new reviews/comments/check runs keep
// arriving and the base branch keeps moving underneath it.
//
// Makes exactly one forge call for PR state (getPRHead) — prior to #340 this
// was two near-identical calls (getPR + getPRHead); see the getPRHead field's
// doc comment.
//
// Returns ok=false if the getPRHead call itself failed or any forge call
// inside ingestPRFeedback failed, so the caller (sweep) can feed per-repo
// consecutive-error backoff (see backoff.go); ok=true (including when
// ingestPRFeedback was never called, e.g. prNumber == 0) otherwise.
func (s *Syncer) syncTask(ctx context.Context, task gen.Task, repo repoInfo) (ok bool) {
	log := slog.With("component", "ghsync", "task_id", task.ID)
	head, err := s.getPRHead(ctx, repo, task.Branch)
	if err != nil {
		log.Warn("ghsync: get PR head", "branch", task.Branch, "err", err)
		return false
	}
	ok = true

	if head.Number != 0 {
		if !s.ingestPRFeedback(ctx, task, repo, head) {
			ok = false
		}
	}

	state := head.State
	if state == task.GitState {
		return ok // no git-state change — nothing further to do
	}

	// Persist the new state, and the PR URL when the live query surfaced one.
	// Keep any previously stored URL if it didn't (e.g. state regressed to a
	// plain "pushed" branch), so we never blank out a valid link.
	storeURL := head.URL
	if storeURL == "" {
		storeURL = task.PrUrl
	}
	updated, err := s.q.SetTaskPR(ctx, gen.SetTaskPRParams{
		GitState: state,
		PrUrl:    storeURL,
		ID:       task.ID,
	})
	if err != nil {
		log.Warn("ghsync: update git state", "err", err)
		return ok
	}

	log.Info("ghsync: git state updated", "old_state", task.GitState, "new_state", state)

	s.hub.Publish("task.git_state_changed", map[string]any{
		"task_id":   task.ID,
		"git_state": state,
		"pr_url":    storeURL,
	})

	// Status write-back to the source GitHub issue (opt-in per repo, no-op if
	// the task wasn't imported or the repo doesn't have it enabled). Both
	// hooks are idempotent via task-row flags, so it's safe to call them
	// unconditionally on every state change rather than only on the specific
	// transition that first satisfies their condition.
	if s.wb != nil {
		s.wb.OnPROpened(ctx, updated, repo.repo)
		s.wb.OnPRMerged(ctx, updated, repo.repo)
	}

	// Once GitHub confirms the PR is merged, the branch's work is preserved
	// upstream and is no longer needed locally — clean it up. Closed-without-
	// merge PRs are left alone so a human can still inspect/reopen the branch.
	if state == "pr_merged" {
		s.cleanupMergedBranch(ctx, task, repo.path)
	}
	return ok
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
