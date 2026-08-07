// Package worktreesweep implements the optional periodic reconciliation of
// each repo's .ate-worktrees/<id> directories against the set of ids that
// legitimately still own one (non-archived tasks and chat sessions),
// reclaiming disk for everything else — worktrees left behind by archiving a
// task on a non-terminal label (nothing else tears those down; see
// cmd/server/main.go's engine.OnTerminal, api/handlers/tasks.go's Delete, and
// ghsync's post-merge cleanup, none of which fire on archive) and worktrees
// orphaned by a crash between provisioning and registering their owner.
//
// This mirrors the scheduler pattern already established by
// internal/logretention and internal/backup: a Sweeper.Run ticks on an
// interval until its context is cancelled, calling RunOnce (safe to call
// directly, e.g. from tests or an admin command) each time.
package worktreesweep

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// MinInterval is the minimum interval a caller is allowed to configure
// between scheduled sweeps (1 minute) — a floor against a misconfigured
// value making the sweeper thrash.
const MinInterval = 1 * time.Minute

// worktreesSubdir is the subdirectory under a repo where per-task/session
// worktrees live. Must match agent.worktreeDir.
const worktreesSubdir = ".ate-worktrees"

// safeIDSegment mirrors agent.safeIDSegment: only delete directory entries
// whose name is a safe single path segment (defense in depth — ids are
// server-generated UUIDs in practice).
var safeIDSegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Sweeper periodically reconciles .ate-worktrees/* across all registered
// repos against the live keep-set (non-archived task ids ∪ chat session
// ids), removing any directory whose name isn't in that set.
type Sweeper struct {
	q        *gen.Queries
	interval time.Duration
}

// New creates a Sweeper that reconciles worktrees every interval.
func New(q *gen.Queries, interval time.Duration) *Sweeper {
	return &Sweeper{q: q, interval: interval}
}

func (s *Sweeper) currentInterval() time.Duration {
	interval := s.interval
	if interval < MinInterval {
		interval = MinInterval
	}
	return interval
}

// Run ticks on the configured interval until ctx is cancelled, calling
// RunOnce each tick. Errors are logged, never fatal.
func (s *Sweeper) Run(ctx context.Context) {
	timer := time.NewTimer(s.currentInterval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := s.RunOnce(ctx); err != nil {
				slog.Error("worktreesweep: sweep failed", "err", err)
			}
			timer.Reset(s.currentInterval())
		}
	}
}

// RunOnce reconciles .ate-worktrees/* for every registered repo against the
// current keep-set. Conservative by design: if any of the list queries used
// to build the keep-set fails, the whole run is skipped (logged, not an
// error) rather than risk deleting a live checkout on partial information.
func (s *Sweeper) RunOnce(ctx context.Context) error {
	repos, err := s.q.ListRepos(ctx)
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}

	tasks, err := s.q.ListTasks(ctx)
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	sessions, err := s.q.ListChatSessions(ctx)
	if err != nil {
		return fmt.Errorf("list chat sessions: %w", err)
	}

	// ids are server-generated UUIDs, so one global keep-set (rather than
	// per-repo) is safe — no cross-repo collision risk.
	keep := make(map[string]struct{}, len(tasks)+len(sessions))
	for _, t := range tasks {
		if t.Archived != 0 {
			continue // archived tasks are exactly what this sweeper reclaims
		}
		keep[t.ID] = struct{}{}
	}
	for _, sess := range sessions {
		keep[sess.ID] = struct{}{}
	}

	for _, repo := range repos {
		s.reconcileRepo(ctx, repo.Path, keep)
	}
	return nil
}

// reconcileRepo removes every entry under repoPath/.ate-worktrees that isn't
// in keep. Best-effort per entry: one failure doesn't stop the rest.
func (s *Sweeper) reconcileRepo(ctx context.Context, repoPath string, keep map[string]struct{}) {
	dir := filepath.Join(repoPath, worktreesSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("worktreesweep: read worktrees dir", "dir", dir, "err", err)
		}
		return
	}

	pruneNeeded := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		if _, ok := keep[id]; ok {
			continue
		}
		if !safeIDSegment.MatchString(id) || id == "." || id == ".." {
			continue // never touch anything that isn't a clean single segment
		}
		wtPath := filepath.Join(dir, id)
		// `git worktree remove --force` (and its os.RemoveAll fallback) mutate
		// the repo's shared ref/worktree-admin store, racing against sibling
		// worktrees' concurrent commits/merges/provisioning if unserialized.
		// Take the per-repo lock (shared with internal/agent's pool, subtask
		// coordinator, dispatcher, and OnTerminal) per entry rather than for the
		// whole loop, so a large sweep doesn't starve live runs waiting on the
		// same repo lock. See internal/agent/worktree.go's repoGitLocks doc
		// comment / issue #344.
		lock := agent.RepoGitLock(repoPath)
		lock.Lock()
		removed := removeWorktree(ctx, repoPath, wtPath)
		lock.Unlock()
		if removed {
			slog.Info("worktreesweep: reclaimed orphaned worktree", "repo", repoPath, "id", id)
			pruneNeeded = true
		}
	}
	if pruneNeeded {
		// A dir removed via os.RemoveAll fallback (crash-orphaned, no longer a
		// registered git worktree) leaves a stale entry in git's internal
		// worktree administration; prune it so `git worktree list` stays clean.
		// `git worktree prune` also mutates worktree administration state, so
		// it's serialized under the same per-repo lock.
		lock := agent.RepoGitLock(repoPath)
		lock.Lock()
		_, _ = exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "prune").CombinedOutput()
		lock.Unlock()
	}
}

// removeWorktree tears down one worktree directory, returning true if it
// removed something. It first tries `git worktree remove --force`, the clean
// path for a directory git still recognizes as a worktree. A directory
// orphaned by a crash may no longer be a registered worktree at all (git
// reports "is not a working tree"), so fall back to a direct os.RemoveAll —
// reconcileRepo then runs `git worktree prune` to clear the stale
// administration entry.
func removeWorktree(ctx context.Context, repoPath, wtPath string) bool {
	if out, err := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "remove", "--force", wtPath).CombinedOutput(); err != nil {
		slog.Warn("worktreesweep: git worktree remove failed; falling back to direct removal", "path", wtPath, "err", err, "out", string(out))
		if rmErr := os.RemoveAll(wtPath); rmErr != nil {
			slog.Warn("worktreesweep: failed to remove orphaned worktree dir", "path", wtPath, "err", rmErr)
			return false
		}
	}
	return true
}
