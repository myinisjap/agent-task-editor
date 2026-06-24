// Package workflow implements the label-based state machine that governs task progression.
package workflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TransitionTrigger identifies who initiated a label change.
type TransitionTrigger string

const (
	TriggerAgent TransitionTrigger = "agent"
	TriggerHuman TransitionTrigger = "human"
)

var (
	ErrNoTransition  = errors.New("no transition defined between these labels")
	ErrGateRequired  = errors.New("transition requires human approval")
	ErrAgentIgnored  = errors.New("label is marked agent_ignore; agents cannot move tasks here")
	ErrTaskNotFound  = errors.New("task not found")
)

// querier is the subset of gen.Queries the engine needs.
type querier interface {
	GetTask(ctx context.Context, id string) (gen.Task, error)
	UpdateTaskLabel(ctx context.Context, arg gen.UpdateTaskLabelParams) (gen.Task, error)
	GetWorkflowTransition(ctx context.Context, arg gen.GetWorkflowTransitionParams) (gen.WorkflowTransition, error)
	ListWorkflowTransitions(ctx context.Context, workflowID string) ([]gen.WorkflowTransition, error)
	ListWorkflowLabels(ctx context.Context, workflowID string) ([]gen.WorkflowLabel, error)
	CreateTaskLabelHistory(ctx context.Context, arg gen.CreateTaskLabelHistoryParams) error
}

// Publisher publishes a workflow event (e.g. to the WebSocket hub).
// The payload is a map that can be JSON-encoded by the caller.
type Publisher interface {
	Publish(eventType string, payload map[string]any)
}

// Engine validates and executes workflow label transitions.
type Engine struct {
	q   querier
	pub Publisher
}

// New creates a new Engine.
func New(db *sql.DB, pub Publisher) *Engine {
	return &Engine{q: gen.New(db), pub: pub}
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
	for _, l := range labels {
		if l.Name == toLabel && l.AgentIgnore != 0 && trigger == TriggerAgent {
			return ErrAgentIgnored
		}
	}

	fromLabel := task.Label
	if _, err := e.q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{
		Label:             toLabel,
		CurrentAgentRunID: task.CurrentAgentRunID,
		ID:                taskID,
	}); err != nil {
		return fmt.Errorf("update task label: %w", err)
	}

	notePtr := (*string)(nil)
	if note != "" {
		notePtr = &note
	}
	actorPtr := (*string)(nil)
	if actorID != "" {
		actorPtr = &actorID
	}
	if err := e.q.CreateTaskLabelHistory(ctx, gen.CreateTaskLabelHistoryParams{
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

	if e.pub != nil {
		e.pub.Publish("task.label_changed", map[string]any{
			"task_id": taskID,
			"from":    fromLabel,
			"to":      toLabel,
			"note":    note,
		})
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
