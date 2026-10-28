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
	// ReasonAutoApproving means the task sits on a human gate with a pending
	// autonomy auto-approval (hold window running). It never also reports
	// human_gate; a person can Hold it.
	ReasonAutoApproving Reason = "auto_approving"
	// ReasonApprovedByPolicy tags RecentPolicyApprovals items (not returned by
	// Compute).
	ReasonApprovedByPolicy Reason = "approved_by_policy"
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
	// EscalationReason is the waiting run's escalation_reason code (why the
	// run went to waiting_human), when recorded. Empty for runs that predate
	// the column and for non-waiting_human items.
	EscalationReason string
	// Since is when the task started needing a human (run creation for
	// waiting_human, otherwise the task's last update).
	Since time.Time
	// DueAt and DecisionID are set for ReasonAutoApproving.
	DueAt      *time.Time
	DecisionID string
	// GateLabel, ApprovedAt, Note, PRURL and Undoable are set for
	// ReasonApprovedByPolicy items.
	GateLabel  string
	ApprovedAt time.Time
	Note       string
	PRURL      string
	Undoable   bool
}

// WorkflowDef is the label/transition graph of one workflow.
type WorkflowDef struct {
	Labels      []gen.WorkflowLabel
	Transitions []gen.WorkflowTransition
}

// Classify returns the primary needs-me item for a task, or ok=false.
// run is the task's active run (nil when none). Precedence:
// waiting_human > pr_conflict > auto_approving > human_gate. pending is the
// task's pending autonomy auto-approval (nil when none); it only counts when it
// is for the task's current label.
func Classify(task gen.Task, run *gen.AgentRun, wf WorkflowDef, pending *gen.AutonomyDecision) (Item, bool) {
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
		if run.EscalationReason != nil {
			it.EscalationReason = *run.EscalationReason
		}
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
		if pending != nil && pending.Label == task.Label {
			it.Reason = ReasonAutoApproving
			it.DueAt = pending.DueAt
			it.DecisionID = pending.ID
			it.Since = pending.CreatedAt
			return it, true
		}
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
	pendingRows, err := q.ListPendingAutonomyApprovals(ctx)
	if err != nil {
		return nil, err
	}
	pending := map[string]*gen.AutonomyDecision{}
	for i := range pendingRows {
		pending[pendingRows[i].TaskID] = &pendingRows[i]
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
		if it, ok := Classify(t, run, wf, pending[t.ID]); ok {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out, nil
}

// IsHumanGate reports whether label is a human gate in wf (the definition used
// for ReasonHumanGate items).
func IsHumanGate(wf WorkflowDef, label string) bool { return isHumanGate(wf, label) }

// LastGateExit finds the most recent label-history row (history is oldest
// first) whose from_label is a human gate in wf: how the task last left a
// gate. ok=false when it never has.
func LastGateExit(history []gen.TaskLabelHistory, wf WorkflowDef) (gen.TaskLabelHistory, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		h := history[i]
		if h.FromLabel != nil && isHumanGate(wf, *h.FromLabel) {
			return h, true
		}
	}
	return gen.TaskLabelHistory{}, false
}

// CanUndo reports whether an auto-approval of task can be undone: its last gate
// exit was made by the policy actor and it is not terminal-merged. It returns
// the gate label to move back to.
func CanUndo(task gen.Task, history []gen.TaskLabelHistory, wf WorkflowDef) (gate string, ok bool, reason string) {
	h, found := LastGateExit(history, wf)
	if !found || h.ActorID == nil || *h.ActorID != "policy" || h.FromLabel == nil {
		return "", false, "the task's last gate exit was not an auto-approval"
	}
	for _, l := range wf.Labels {
		if l.Name == task.Label && l.IsTerminal != 0 && (task.GitState == "pr_merged" || task.MergeStatus == "merged") {
			return "", false, "task is already merged; cannot undo"
		}
	}
	if task.Label == *h.FromLabel {
		return "", false, "task is already on the gate"
	}
	return *h.FromLabel, true, ""
}

// RecentPolicyApprovals returns the tasks the autonomy policy approved in the
// 24 hours before now (newest first, latest approval per task, archived and
// deleted tasks skipped) for the "Approved for you" list.
func RecentPolicyApprovals(ctx context.Context, q *gen.Queries, now time.Time) ([]Item, error) {
	cutoff := now.Add(-24 * time.Hour)
	rows, err := q.ListRecentPolicyApprovals(ctx, &cutoff)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	wfs := map[string]WorkflowDef{}
	out := []Item{}
	for _, d := range rows {
		if seen[d.TaskID] {
			continue
		}
		seen[d.TaskID] = true
		t, err := q.GetTask(ctx, d.TaskID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
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
		hist, err := q.ListTaskLabelHistory(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		_, undoable, _ := CanUndo(t, hist, wf)
		it := Item{
			TaskID: t.ID, TaskTitle: t.Title, RepoID: t.RepoID, WorkflowID: t.WorkflowID, Label: t.Label,
			Reason: ReasonApprovedByPolicy, DecisionID: d.ID, GateLabel: d.Label, PRURL: t.PrUrl, Undoable: undoable,
		}
		if d.OutcomeAt != nil {
			it.ApprovedAt = *d.OutcomeAt
			it.Since = *d.OutcomeAt
		}
		if d.Note != nil {
			it.Note = *d.Note
		}
		out = append(out, it)
	}
	return out, nil
}
