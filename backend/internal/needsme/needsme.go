// Package needsme defines, in one place, which tasks need a human and why.
// The Dashboard queue, board card badges and any future inbox/notification
// surface all derive from Compute so they cannot disagree.
package needsme

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// Reason is the primary reason a task needs a human.
type Reason string

const (
	// ReasonWaitingHuman means the task's active run is waiting_human.
	ReasonWaitingHuman Reason = "waiting_human"
	// ReasonPRConflict means the task's open PR conflicts with its base branch.
	ReasonPRConflict Reason = "pr_conflict"
	// ReasonHumanGate means the task sits on a label only a human can move it off.
	ReasonHumanGate Reason = "human_gate"
)

// Item is one task that needs a human.
type Item struct {
	TaskID     string
	TaskTitle  string
	RepoID     string
	WorkflowID string
	Label      string
	Reason     Reason
	// Message is the waiting run's feedback/notes text, when present.
	Message string
	// RunID is set for ReasonWaitingHuman.
	RunID string
	// Since is when the task started needing a human (run creation for
	// waiting_human, otherwise the task's last update).
	Since time.Time
}

// WorkflowDef is the label/transition graph of one workflow.
type WorkflowDef struct {
	Labels      []gen.WorkflowLabel
	Transitions []gen.WorkflowTransition
}

// Classify returns the primary needs-me item for a task, or ok=false.
// run is the task's active run (nil when none). Precedence:
// waiting_human > pr_conflict > human_gate.
func Classify(task gen.Task, run *gen.AgentRun, wf WorkflowDef) (Item, bool) {
	if task.Archived != 0 {
		return Item{}, false
	}
	base := Item{TaskID: task.ID, TaskTitle: task.Title, RepoID: task.RepoID, WorkflowID: task.WorkflowID, Label: task.Label, Since: task.UpdatedAt}

	if run != nil && run.Status == "waiting_human" {
		it := base
		it.Reason = ReasonWaitingHuman
		it.RunID = run.ID
		it.Since = run.CreatedAt
		it.Message = firstNonEmpty(run.Feedback, run.Notes)
		return it, true
	}
	if task.Paused != 0 {
		return Item{}, false
	}
	if run != nil && (run.Status == "pending" || run.Status == "running") {
		return Item{}, false
	}
	if task.GitState == "pr_open" && task.PrMergeable == "conflicting" {
		it := base
		it.Reason = ReasonPRConflict
		return it, true
	}
	if isHumanGate(wf, task.Label) {
		it := base
		it.Reason = ReasonHumanGate
		return it, true
	}
	return Item{}, false
}

// isHumanGate mirrors the dispatcher/notify definition (workflow.IsHumanGateLabel:
// no agent-triggerable way out) and additionally requires a human-triggered
// outbound transition, so dead-end labels are not reported. agent_ignore
// labels (the backlog/parking column, e.g. not_ready) are excluded: tasks
// parked there are not awaiting a decision.
func isHumanGate(wf WorkflowDef, label string) bool {
	for _, l := range wf.Labels {
		if l.Name == label && l.AgentIgnore != 0 {
			return false
		}
	}
	if !workflow.IsHumanGateLabel(wf.Labels, wf.Transitions, label) {
		return false
	}
	for _, t := range wf.Transitions {
		if t.FromLabel == label && t.TriggerType == "human" {
			return true
		}
	}
	return false
}

func firstNonEmpty(ps ...*string) string {
	for _, p := range ps {
		if p != nil && *p != "" {
			return *p
		}
	}
	return ""
}

// Compute classifies every task and returns the needs-me items oldest first.
func Compute(ctx context.Context, q *gen.Queries) ([]Item, error) {
	tasks, err := q.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	wfs := map[string]WorkflowDef{}
	out := []Item{}
	for _, t := range tasks {
		if t.Archived != 0 {
			continue
		}
		wf, ok := wfs[t.WorkflowID]
		if !ok {
			labels, err := q.ListWorkflowLabels(ctx, t.WorkflowID)
			if err != nil {
				return nil, err
			}
			trans, err := q.ListWorkflowTransitions(ctx, t.WorkflowID)
			if err != nil {
				return nil, err
			}
			wf = WorkflowDef{Labels: labels, Transitions: trans}
			wfs[t.WorkflowID] = wf
		}
		var run *gen.AgentRun
		if t.ActiveAgentRunID != nil {
			r, err := q.GetAgentRun(ctx, *t.ActiveAgentRunID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if err == nil {
				run = &r
			}
		}
		if it, ok := Classify(t, run, wf); ok {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out, nil
}
