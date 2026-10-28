package reviewbrief

import (
	"context"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// DiffAsOf computes the diff-derived summary and flags for the task's branch
// as it stood at time at (the newest branch commit dated at or before it),
// using the same TaskDiff + flag rules as the live brief. It is read-only and
// uncached. Available=false when the branch, base or a commit as of that time
// cannot be resolved.
func DiffAsOf(ctx context.Context, q *gen.Queries, task gen.Task, at time.Time) (DiffResult, error) {
	empty := DiffResult{Changed: ChangeSummary{TopFiles: []FileStat{}}, Flags: []Flag{}}
	if task.Branch == "" || !gitcmd.IsValidRef(task.Branch) || !gitcmd.IsValidRef(task.BaseRef) {
		return empty, nil
	}
	gctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	gitDir, err := GitDir(gctx, q, task)
	if err != nil {
		return empty, err
	}
	if _, err := gitcmd.Output(gctx, gitDir, "rev-parse", "--verify", "--quiet", task.BaseRef); err != nil {
		return empty, nil
	}
	out, err := gitcmd.Output(gctx, gitDir, "rev-list", "-1", "--before="+at.UTC().Format("2006-01-02T15:04:05Z"), task.Branch)
	sha := strings.TrimSpace(string(out))
	if err != nil || sha == "" {
		return empty, nil
	}
	diff, err := TaskDiff(gctx, gitDir, task.BaseRef, sha)
	if err != nil {
		return empty, nil
	}
	return compute(diff, task.AgentNotes), nil
}
