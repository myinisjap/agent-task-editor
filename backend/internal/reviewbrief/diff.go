// Package reviewbrief builds the deterministic "review brief" shown to a human
// when a task reaches a human gate: risk flags computed from the branch diff,
// plus a summary of verification, runs and the last agent summary. It makes no
// LLM calls; flags are advisory only.
package reviewbrief

import (
	"context"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// GitDir returns the directory git commands for the task should run in: the
// task's worktree when it still exists, else the repo's main clone (terminal
// tasks have their worktree removed but the branch is kept).
func GitDir(ctx context.Context, q *gen.Queries, task gen.Task) (string, error) {
	if task.WorktreePath != "" && gitcmd.DirExists(task.WorktreePath) {
		return task.WorktreePath, nil
	}
	repo, err := q.GetRepo(ctx, task.RepoID)
	if err != nil {
		return "", err
	}
	return repo.Path, nil
}

// TaskDiff returns the diff of branch against its merge-base with baseRef
// (falling back to baseRef itself when no merge-base is found).
func TaskDiff(ctx context.Context, gitDir, baseRef, branch string) (string, error) {
	mb, err := gitcmd.Output(ctx, gitDir, "merge-base", baseRef, branch)
	base := baseRef
	if err == nil {
		if s := strings.TrimSpace(string(mb)); s != "" {
			base = s
		}
	}
	out, err := gitcmd.Output(ctx, gitDir, "diff", base, branch, "--")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
