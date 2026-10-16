package agent

import (
	"context"
	"fmt"

	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TaskTerminalStore is the slice of *gen.Queries that OnTaskTerminal needs to
// clear a stale worktree_path after cleanup. Declared as an interface so
// tests can supply a minimal fake; the production caller always passes a
// *gen.Queries.
type TaskTerminalStore interface {
	ClearTaskWorktreePath(ctx context.Context, id string) error
}

// OnTaskTerminal is the non-subtask body of the workflow engine's OnTerminal
// side effect: for an ordinary task reaching a terminal label, push its
// branch (if the repo has a remote) and tear down its worktree to reclaim
// disk — the branch itself is kept for human review/PR. Subtasks are handled
// separately by SubtaskCoordinator.OnChildTerminal (merge-back into the
// parent instead of pushing to origin).
//
// Pushing is skipped entirely once the task's PR is already merged or closed
// (git_state "pr_merged"/"pr_closed"): the work is already upstream (or
// deliberately abandoned), and ghsync's cleanupMergedBranch may have already
// deleted the remote branch on GitHub's side, so re-pushing could resurrect
// it there. That same cleanup also removes the worktree directory and clears
// task.WorktreePath (see ghsync/syncer.go), so the common merged case returns
// immediately below with nothing to do. This function tolerates a stale
// WorktreePath regardless — e.g. a crash between ghsync's two steps, or a run
// that reaches terminal before ghsync's next poll — by running git commands
// from the repo's main clone (where the branch ref still lives) whenever the
// worktree directory itself is gone, mirroring prflow.GitDir's fallback.
func OnTaskTerminal(ctx context.Context, q TaskTerminalStore, task gen.Task, repo gen.Repo) error {
	if task.WorktreePath == "" {
		return nil
	}
	merged := task.GitState == "pr_merged" || task.GitState == "pr_closed"

	gitDir := task.WorktreePath
	if !gitcmd.DirExists(gitDir) {
		gitDir = repo.Path
	}

	// PushBranch and RemoveWorktree mutate the repo's shared ref store;
	// serialize against sibling worktrees' concurrent git writes (safety-net
	// commits, subtask merges, dispatcher provisioning, the sweeper) via the
	// per-repo lock.
	lock := RepoGitLock(repo.Path)
	lock.Lock()
	defer lock.Unlock()

	if !merged && repo.RemoteUrl != nil && *repo.RemoteUrl != "" && task.Branch != "" {
		if err := PushBranch(ctx, gitDir, task.Branch); err != nil {
			return fmt.Errorf("push branch %s: %w", task.Branch, err)
		}
	}
	if err := RemoveWorktree(ctx, repo.Path, task.WorktreePath); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	if err := q.ClearTaskWorktreePath(ctx, task.ID); err != nil {
		return fmt.Errorf("clear worktree_path: %w", err)
	}
	return nil
}
