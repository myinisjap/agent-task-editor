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
	// Runtime must be the SAME *agent.RuntimeManager instance passed to
	// Dispatcher.Runtime (see cmd/server/main.go), not a second one — its
	// per-repo mutex only actually serializes EnsureRunning against
	// reconcileContainers if both call sites share one instance. Currently
	// used only as a nil-check guard (reconcileContainers itself compares
	// each repo's runtime_image directly against the ate.image label, no
	// RuntimeManager method calls needed); kept as a required constructor
	// param so a future reconcileContainers change that does need it can't
	// silently run against a nil Runtime the way this field's prior
	// optional-field form did (see issue: sweeper reaped healthy containers
	// because nothing ever set it).
	Runtime *agent.RuntimeManager
}

// New creates a Sweeper that reconciles worktrees every interval. runtime
// must be the same instance as the dispatcher's RuntimeManager — see the
// Sweeper.Runtime doc comment.
func New(q *gen.Queries, interval time.Duration, runtime *agent.RuntimeManager) *Sweeper {
	return &Sweeper{q: q, interval: interval, Runtime: runtime}
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
	// reposWithActiveRun collects every repo id with at least one task whose
	// active_agent_run_id is non-nil (set by the dispatcher, cleared only on
	// label transition or run completion — see AGENTS.md's Architecture
	// Decision Notes). Used by reconcileContainers to avoid reaping a
	// container mid-run: see that function's doc comment.
	reposWithActiveRun := make(map[string]struct{})
	for _, t := range tasks {
		if t.ActiveAgentRunID != nil {
			reposWithActiveRun[t.RepoID] = struct{}{}
		}
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

	s.reconcileContainers(ctx, repos, reposWithActiveRun)
	return nil
}

// repoRuntimeState is a repo's current expected runtime-container identity,
// used by shouldReapContainer to decide whether a live container still
// matches what that repo would produce today.
type repoRuntimeState struct {
	Image string
}

// resolveRepoRuntimeState computes each repo's current expected runtime
// identity: an explicit RuntimeImage (mirrors dispatcher.go's resolution
// order). A repo with no RuntimeImage set gets the zero value — any
// container found for it is therefore always reaped, which is correct: no
// source means no container should exist for that repo at all.
func (s *Sweeper) resolveRepoRuntimeState(repos []gen.Repo) map[string]repoRuntimeState {
	states := make(map[string]repoRuntimeState, len(repos))
	for _, r := range repos {
		states[r.ID] = repoRuntimeState{Image: r.RuntimeImage}
	}
	return states
}

// reconcileContainers removes every ate.repo_id-labeled runtime container
// (see internal/agent/runtime.go) whose repo no longer exists, or whose
// ate.image label no longer matches that repo's current runtime_image —
// mirroring reconcileRepo's job for worktree directories, but for the
// per-repo containers RuntimeManager.EnsureRunning creates. A container is
// left alone (not reaped) when its repo still exists and its label still
// matches — the dispatcher's EnsureRunning path itself handles recreating a
// container in that case, on its own next-dispatch path; this sweep only
// cleans up what dispatch will never revisit (repo gone) or would otherwise
// leave running stale until the next dispatch happens to land on that repo
// (image changed).
//
// reposWithActiveRun (built by RunOnce from the same task list used for the
// worktree keep-set) also protects a container whose repo has a task
// currently locked on a run: EnsureRunning resolves the container name at
// startRun, well before the provider actually `docker exec`s into it (pool
// enqueue, prompt building, and MCP prep all happen in between — minutes
// under queue backpressure). Reaping the container in that window kills an
// in-flight run with a raw "No such container" error. A repo with an active
// run is skipped entirely this tick even if its image is otherwise stale;
// it gets reaped on a later tick once the repo goes idle.
//
// Best-effort and non-fatal like the rest of this package: docker not being
// installed/reachable is the common case for anyone not using per-repo
// runtime containers at all, so a ListManagedContainers error here is logged
// at Warn (not Error) and simply skips this pass for the tick.
func (s *Sweeper) reconcileContainers(ctx context.Context, repos []gen.Repo, reposWithActiveRun map[string]struct{}) {
	containers, err := agent.ListManagedContainers(ctx)
	if err != nil {
		slog.Warn("worktreesweep: list runtime containers (skipping container reap this tick)", "err", err)
		return
	}
	if len(containers) == 0 {
		return
	}

	states := s.resolveRepoRuntimeState(repos)

	for _, c := range containers {
		if !shouldReapContainer(c, states, reposWithActiveRun) {
			continue
		}
		if err := agent.RemoveContainer(ctx, c.Name); err != nil {
			slog.Warn("worktreesweep: failed to remove stale runtime container", "container", c.Name, "repo_id", c.RepoID, "err", err)
			continue
		}
		_, repoExists := states[c.RepoID]
		slog.Info("worktreesweep: reaped runtime container", "container", c.Name, "repo_id", c.RepoID, "repo_still_exists", repoExists)
	}
}

// shouldReapContainer is the pure keep-vs-reap decision for one managed
// container, factored out of reconcileContainers so it's unit-testable
// without a Docker daemon.
//
// A container with an in-flight run (its repo has a task with a non-nil
// active_agent_run_id) is always kept, regardless of image staleness or the
// repo even still existing: EnsureRunning resolves the container name at
// startRun, well before the provider actually `docker exec`s into it (pool
// enqueue, prompt building, and MCP prep all happen in between — minutes
// under queue backpressure). Reaping the container in that window kills an
// in-flight run with a raw "No such container" error. It gets reaped on a
// later tick once the repo goes idle.
//
// Otherwise, a container is kept only when its repo still exists AND its
// Image label matches that repo's current, non-empty expected Image.
// Anything else — repo gone, image stale, or the repo's current expected
// image cleared back to empty — is reaped. This enforces the
// non-negotiable "empty runtime_image behaves as before" rule for
// containers left running from before the field was cleared.
func shouldReapContainer(c agent.ManagedContainer, states map[string]repoRuntimeState, reposWithActiveRun map[string]struct{}) bool {
	if _, active := reposWithActiveRun[c.RepoID]; active {
		return false
	}
	state, repoExists := states[c.RepoID]
	if !repoExists {
		return true
	}
	return state.Image == "" || state.Image != c.Image
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
