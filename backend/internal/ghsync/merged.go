package ghsync

import (
	"context"
	"log/slog"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// mergedNote is the label-history note recorded when a merged PR finishes a
// task.
const mergedNote = "PR merged"

// finishMergedTask moves a task whose PR just merged onto its workflow's
// terminal label. A merge means the work is done and cleanupMergedBranch has
// already removed the worktree, so a task left on a working label would just
// sit there: typically with a waiting_human run from a CI-failure bounce that
// can never resume. The move goes through ForceTransition because workflows
// rarely define an edge from every working label to the terminal one; like any
// label move it releases the waiting run's dispatch lock.
//
// A live (pending/running) run is left alone, the same guard human moves use
// (see handlers.runLiveStatuses, ADR 0003): clearing the lock under it would
// let the dispatcher start a second run. No-op without an engine, when the
// task is already on a terminal label, or when the workflow has none.
func (s *Syncer) finishMergedTask(ctx context.Context, task gen.Task, log *slog.Logger) {
	if s.engine == nil {
		return
	}
	labels, err := s.q.ListWorkflowLabels(ctx, task.WorkflowID)
	if err != nil {
		log.Warn("ghsync: list workflow labels for merged task", "err", err)
		return
	}
	terminal := ""
	for _, l := range labels {
		if l.IsTerminal == 0 {
			continue
		}
		if l.Name == task.Label {
			return
		}
		if terminal == "" {
			terminal = l.Name
		}
	}
	if terminal == "" {
		log.Info("ghsync: merged task's workflow has no terminal label, leaving it in place", "label", task.Label)
		return
	}
	if task.ActiveAgentRunID != nil {
		run, err := s.q.GetAgentRun(ctx, *task.ActiveAgentRunID)
		if err == nil && (run.Status == "pending" || run.Status == "running") {
			log.Info("ghsync: merged task has a live run, leaving it in place", "run_id", run.ID, "status", run.Status)
			return
		}
	}
	if err := s.engine.ForceTransition(ctx, task.ID, terminal, "", mergedNote); err != nil {
		log.Warn("ghsync: move merged task to terminal label", "to_label", terminal, "err", err)
		return
	}
	log.Info("ghsync: moved merged task to terminal label", "from_label", task.Label, "to_label", terminal)
}
