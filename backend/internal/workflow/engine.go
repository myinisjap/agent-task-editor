// Package workflow implements the label-based state machine that governs task progression.
package workflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// GateLabel returns a workflow's human-gate landing label — the label a task
// should start on so a human promotes it before an agent picks it up. That is
// the lowest-sort_order agent_ignore label (gate); first is the lowest-sort_order
// label overall, used as a fallback when the workflow defines no agent_ignore
// label. Both are "" when the workflow has no labels. For the default seeded
// workflow the gate is "not_ready"; this generalizes that convention to any
// workflow instead of hard-coding the name.
func GateLabel(labels []gen.WorkflowLabel) (gate, first string) {
	sorted := append([]gen.WorkflowLabel(nil), labels...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].SortOrder < sorted[j].SortOrder })
	for _, l := range sorted {
		if first == "" {
			first = l.Name
		}
		if l.AgentIgnore != 0 {
			return l.Name, first
		}
	}
	return "", first
}

// TransitionTrigger identifies who initiated a label change.
type TransitionTrigger string

const (
	TriggerAgent TransitionTrigger = "agent"
	TriggerHuman TransitionTrigger = "human"
	// TriggerSubtasksComplete marks the automatic advance of a parent task once
	// all of its subtasks reach a terminal label and merge back cleanly. It is
	// recorded in task_label_history as its own trigger so the auto-advance is
	// distinguishable from a normal agent/human move. Because it is neither
	// "agent" nor "human", it bypasses the human-gate / agent_ignore checks — the
	// coordinator only ever uses it for a transition it already validated as the
	// parent's agent-success path.
	TriggerSubtasksComplete TransitionTrigger = "subtasks_complete"
)

var (
	ErrNoTransition  = errors.New("no transition defined between these labels")
	ErrGateRequired  = errors.New("transition requires human approval")
	ErrAgentIgnored  = errors.New("label is marked agent_ignore; agents cannot move tasks here")
	ErrTaskNotFound  = errors.New("task not found")
	// ErrStale means the task's label changed out from under this transition
	// between validation and the compare-and-swap write — a concurrent transition
	// won the race. Callers should refresh and retry.
	ErrStale = errors.New("task label changed concurrently; transition is stale")
)

// querier is the subset of gen.Queries the engine needs.
type querier interface {
	GetTask(ctx context.Context, id string) (gen.Task, error)
	UpdateTaskLabel(ctx context.Context, arg gen.UpdateTaskLabelParams) (gen.Task, error)
	GetWorkflowTransition(ctx context.Context, arg gen.GetWorkflowTransitionParams) (gen.WorkflowTransition, error)
	ListWorkflowTransitions(ctx context.Context, workflowID string) ([]gen.WorkflowTransition, error)
	ListWorkflowLabels(ctx context.Context, workflowID string) ([]gen.WorkflowLabel, error)
	CreateTaskLabelHistory(ctx context.Context, arg gen.CreateTaskLabelHistoryParams) error
	ListTaskDependents(ctx context.Context, dependsOnTaskID string) ([]gen.ListTaskDependentsRow, error)
}

// Publisher publishes a workflow event (e.g. to the WebSocket hub).
// The payload is a map that can be JSON-encoded by the caller.
type Publisher interface {
	Publish(eventType string, payload map[string]any)
}

// Engine validates and executes workflow label transitions.
type Engine struct {
	db  *sql.DB
	q   querier
	pub Publisher
	// OnTerminal, if set, is called after a task successfully transitions into a
	// terminal label. Used to push the task's branch and tear down its worktree.
	// Failures are the callback's concern; the transition has already committed.
	OnTerminal func(ctx context.Context, task gen.Task)
	// OnLeaveNotReady, if set, is called after a task successfully transitions
	// off the workflow's human-gate label — the label GateLabel resolves to,
	// which is "not_ready" for the default workflow but generalizes to any
	// workflow — for the first time (i.e. fromLabel is the gate label and
	// toLabel is not). Used to apply an "agent is already working on this" signal
	// to an imported task's source GitHub issue. Failures are the callback's
	// concern; the transition has already committed.
	OnLeaveNotReady func(ctx context.Context, task gen.Task)
	// OnCreatePR, if set, is called after a task transitions into a label marked
	// create_pr, regardless of who triggered the move. Used to push the task's
	// branch and open a GitHub pull request. Failures are the callback's concern;
	// the transition has already committed.
	OnCreatePR func(ctx context.Context, task gen.Task)
}

// New creates a new Engine.
func New(db *sql.DB, pub Publisher) *Engine {
	return &Engine{db: db, q: gen.New(db), pub: pub}
}

// Transition validates and executes a label change for a task.
//
//   - Returns ErrNoTransition if (from→to) is not defined in the workflow.
//   - Returns ErrGateRequired if the transition requires human input but trigger is agent.
//   - Returns ErrAgentIgnored if the destination label has agent_ignore set.
func (e *Engine) Transition(ctx context.Context, taskID, toLabel string, trigger TransitionTrigger, actorID, note string) error {
	task, err := e.q.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}

	trans, err := e.q.GetWorkflowTransition(ctx, gen.GetWorkflowTransitionParams{
		WorkflowID: task.WorkflowID,
		FromLabel:  task.Label,
		ToLabel:    toLabel,
	})
	if err != nil {
		return ErrNoTransition
	}

	if trigger == TriggerAgent && trans.TriggerType == "human" {
		return ErrGateRequired
	}

	labels, err := e.q.ListWorkflowLabels(ctx, task.WorkflowID)
	if err != nil {
		return fmt.Errorf("list labels: %w", err)
	}
	toIsTerminal := false
	toCreatePR := false
	for _, l := range labels {
		if l.Name == toLabel {
			if l.AgentIgnore != 0 && trigger == TriggerAgent {
				return ErrAgentIgnored
			}
			toIsTerminal = l.IsTerminal != 0
			toCreatePR = l.CreatePr != 0
		}
	}

	fromLabel := task.Label

	notePtr := (*string)(nil)
	if note != "" {
		notePtr = &note
	}
	actorPtr := (*string)(nil)
	if actorID != "" {
		actorPtr = &actorID
	}

	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	tq := gen.New(tx)
	// Compare-and-swap on the label: only update if the task is still on the
	// label we validated against (fromLabel). If a concurrent transition already
	// moved it, this matches 0 rows and we report a stale conflict rather than
	// silently clobbering the other write. Run as raw SQL (mirroring the
	// generated UpdateTaskLabel, plus the `AND label = ?` guard and always
	// clearing active_agent_run_id) because sqlc's SQLite analyzer miscompiles
	// this particular query — see the byte-offset note on SearchTasksPage.
	res, err := tx.ExecContext(ctx,
		`UPDATE tasks SET label = ?, current_agent_run_id = ?, active_agent_run_id = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND label = ?`,
		toLabel, task.CurrentAgentRunID, taskID, fromLabel)
	if err != nil {
		return fmt.Errorf("update task label: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("update task label: %w", err)
	} else if n == 0 {
		return ErrStale
	}
	if err := tq.CreateTaskLabelHistory(ctx, gen.CreateTaskLabelHistoryParams{
		ID:        uuid.NewString(),
		TaskID:    taskID,
		FromLabel: &fromLabel,
		ToLabel:   toLabel,
		Trigger:   string(trigger),
		ActorID:   actorPtr,
		Note:      notePtr,
	}); err != nil {
		return fmt.Errorf("record history: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if e.pub != nil {
		e.pub.Publish("task.label_changed", map[string]any{
			"task_id": taskID,
			"from":    fromLabel,
			"to":      toLabel,
			"note":    note,
		})
		// A label change can satisfy or un-satisfy edges that point at this task
		// (e.g. moving into or out of a terminal label). Nudge dependents so their
		// "blocked by N" badges refresh live instead of going stale on the board.
		if deps, derr := e.q.ListTaskDependents(ctx, taskID); derr == nil {
			for _, d := range deps {
				e.pub.Publish("task.updated", map[string]any{"id": d.TaskID})
			}
		}
	}

	if toIsTerminal && e.OnTerminal != nil {
		task.Label = toLabel
		e.OnTerminal(ctx, task)
	}

	if toCreatePR && e.OnCreatePR != nil {
		task.Label = toLabel
		e.OnCreatePR(ctx, task)
	}

	// Fire OnLeaveNotReady when a task first moves off the workflow's human-gate
	// label (GateLabel — "not_ready" for the default workflow, the equivalent
	// gate for any custom one). We deliberately key only on the real agent_ignore
	// gate, not GateLabel's first-label fallback: the "agent-in-progress"
	// write-back signals that a task has left a human-review column, which is
	// only meaningful when such a column exists. A workflow with no agent_ignore
	// label has no human gate to leave, so the hook stays a no-op there.
	if gate, _ := GateLabel(labels); gate != "" && fromLabel == gate && toLabel != gate && e.OnLeaveNotReady != nil {
		task.Label = toLabel
		e.OnLeaveNotReady(ctx, task)
	}

	return nil
}

// AvailableTransitions returns the labels a task can move to from its current state,
// filtered to those permitted for the given trigger type.
func (e *Engine) AvailableTransitions(ctx context.Context, taskID string, trigger TransitionTrigger) ([]string, error) {
	task, err := e.q.GetTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}

	all, err := e.q.ListWorkflowTransitions(ctx, task.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("list transitions: %w", err)
	}

	var out []string
	for _, t := range all {
		if t.FromLabel != task.Label {
			continue
		}
		if trigger == TriggerAgent && t.TriggerType == "human" {
			continue
		}
		out = append(out, t.ToLabel)
	}
	return out, nil
}

// AgentPickupLabels returns all labels in a workflow where agents are allowed to
// initiate transitions (trigger_type = 'agent' or 'both') and the label itself
// is not marked agent_ignore.
func (e *Engine) AgentPickupLabels(ctx context.Context, workflowID string) ([]string, error) {
	labels, err := e.q.ListWorkflowLabels(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	agentIgnored := map[string]bool{}
	for _, l := range labels {
		if l.AgentIgnore != 0 {
			agentIgnored[l.Name] = true
		}
	}

	transitions, err := e.q.ListWorkflowTransitions(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("list transitions: %w", err)
	}

	seen := map[string]bool{}
	var out []string
	for _, t := range transitions {
		if t.TriggerType == "human" {
			continue
		}
		if agentIgnored[t.FromLabel] {
			continue
		}
		if !seen[t.FromLabel] {
			seen[t.FromLabel] = true
			out = append(out, t.FromLabel)
		}
	}
	return out, nil
}
