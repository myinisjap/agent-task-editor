package ghsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/automerge"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// terminalRerouteLimit caps how many times a task is routed from a terminal
// label back to the PR fixer, so a persistently red PR cannot loop forever.
const terminalRerouteLimit = 3

// terminalRerouteNote builds the label-history note for a reroute. It embeds
// the full head SHA; noteIsForHead relies on that for same-head dedupe.
func terminalRerouteNote(task gen.Task, msg, sha, target, prLabel string) string {
	return fmt.Sprintf("Auto-merge blocked on terminal label %q (%s) at %s: routed to %q (pr_feedback_label of %q) for the PR fixer",
		task.Label, msg, sha, target, prLabel)
}

func noteIsForHead(note *string, sha string) bool {
	return note != nil && sha != "" && strings.Contains(*note, " at "+sha+":")
}

// routeTerminalPRToFixer handles a task sitting on a terminal label whose
// open PR has auto-merge blocked by failed checks or a merge conflict. The
// regular feedback path (autoTransitionOnFeedback) only looks at the current
// label's pr_feedback_label, which terminal labels cannot have, so without
// this the task would stall. It moves the task to the pr_feedback_label of the
// create_pr label that opened the PR. Enabling auto-merge (when_green) is the
// opt-in; pr_review_auto_transition_enabled is not required. Best effort.
func (s *Syncer) routeTerminalPRToFixer(ctx context.Context, task gen.Task, repo repoInfo, head forge.PRHead, decision automerge.Decision) {
	if s.engine == nil || s.q == nil {
		return
	}
	if automerge.Mode(repo.repo) != automerge.ModeWhenGreen || !decision.Applies ||
		(decision.Reason != automerge.ReasonChecksFailed && decision.Reason != automerge.ReasonConflict) {
		return
	}
	// head.State is the live value (syncTask persists git_state after this).
	if head.State != "pr_open" {
		return
	}
	log := slog.With("component", "ghsync", "task_id", task.ID)
	fresh, err := s.q.GetTask(ctx, task.ID)
	if err != nil {
		return
	}
	task = fresh
	labels, err := s.q.ListWorkflowLabels(ctx, task.WorkflowID)
	if err != nil {
		return
	}
	byName := make(map[string]gen.WorkflowLabel, len(labels))
	for _, l := range labels {
		byName[l.Name] = l
	}
	cur, ok := byName[task.Label]
	if !ok || cur.IsTerminal == 0 {
		return
	}
	hist, err := s.q.ListTaskLabelHistory(ctx, task.ID)
	if err != nil {
		return
	}

	// PR-opening label: newest history entry that landed on a create_pr label.
	prLabel := ""
	for i := len(hist) - 1; i >= 0 && prLabel == ""; i-- {
		if l, ok := byName[hist[i].ToLabel]; ok && l.CreatePr != 0 {
			prLabel = l.Name
		}
	}
	if prLabel == "" {
		n := 0
		for _, l := range labels {
			if l.CreatePr != 0 {
				prLabel = l.Name
				n++
			}
		}
		if n != 1 {
			return
		}
	}
	target := strings.TrimSpace(byName[prLabel].PrFeedbackLabel)
	if target == "" {
		return
	}
	if t, ok := byName[target]; !ok || t.IsTerminal != 0 {
		return
	}

	// Breakers: our own history rows are terminal -> X moves by the
	// pr-feedback-sync actor (the validator forbids pr_feedback_label on
	// terminal labels, so nothing else produces them).
	count := 0
	var newest *gen.TaskLabelHistory
	for i := range hist {
		h := hist[i]
		if h.ActorID == nil || *h.ActorID != workflow.ActorPRFeedbackSync || h.FromLabel == nil {
			continue
		}
		if f, ok := byName[*h.FromLabel]; ok && f.IsTerminal != 0 {
			count++
			newest = &hist[i]
		}
	}
	if count >= terminalRerouteLimit {
		log.Warn("ghsync: persistently red PR on terminal task; leaving for a human", "reroutes", count)
		return
	}
	if newest != nil && noteIsForHead(newest.Note, head.HeadSHA) {
		log.Debug("ghsync: terminal task already rerouted for this head commit", "head_sha", head.HeadSHA)
		return
	}
	if s.engine.RunIsLive(ctx, task) {
		return
	}
	effects, err := s.q.ListTaskSideEffectsForTasks(ctx, []string{task.ID})
	if err != nil {
		return
	}
	for _, e := range effects {
		if e.Kind == workflow.SideEffectTerminal && e.Status != "done" {
			log.Info("ghsync: terminal side effect not finished; not rerouting yet", "status", e.Status)
			return
		}
	}

	note := terminalRerouteNote(task, decision.Message, head.HeadSHA, target, prLabel)
	if err := s.engine.MoveBack(ctx, task.ID, target, workflow.ActorPRFeedbackSync, note, task.Label); err != nil {
		log.Info("ghsync: reroute terminal task to PR fixer skipped", "err", err, "stale", errors.Is(err, workflow.ErrStale))
		return
	}
	log.Info("ghsync: routed terminal task with blocked PR to PR fixer",
		"from_label", task.Label, "to_label", target, "reason", decision.Reason, "head_sha", head.HeadSHA)
	s.appendRunFeedback(ctx, task, "Auto-merge is blocked: "+decision.Message+". Fix the PR so CI passes / it merges cleanly.", log)
	s.hub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: task.ID})
}
