package storage

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// SeedDefaultWorkflow inserts the default workflow if no workflows exist.
func SeedDefaultWorkflow(ctx context.Context, db *DB) error {
	q := gen.New(db.SQL())

	count, err := q.CountWorkflows(ctx)
	if err != nil {
		return fmt.Errorf("count workflows: %w", err)
	}
	if count > 0 {
		return nil
	}

	wfID := uuid.NewString()
	_, err = q.CreateWorkflow(ctx, gen.CreateWorkflowParams{
		ID:          wfID,
		Name:        "Default",
		Description: "Default agentic code editor workflow",
	})
	if err != nil {
		return fmt.Errorf("create workflow: %w", err)
	}

	labels := []struct {
		name        string
		color       string
		order       int
		agentIgnore bool
		terminal    bool
	}{
		{"not_ready", "#6B7280", 0, true, false},
		{"plan", "#8B5CF6", 1, false, false},
		{"review-plan", "#3B82F6", 2, false, false},
		{"work", "#F59E0B", 3, false, false},
		{"testing", "#F97316", 4, false, false},
		{"agent-review", "#6366F1", 5, false, false},
		{"review", "#EC4899", 6, false, false},
		{"done", "#10B981", 7, false, true},
	}

	for _, l := range labels {
		agentIgnore := int64(0)
		if l.agentIgnore {
			agentIgnore = 1
		}
		isTerminal := int64(0)
		if l.terminal {
			isTerminal = 1
		}
		if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
			ID:          uuid.NewString(),
			WorkflowID:  wfID,
			Name:        l.name,
			Color:       l.color,
			SortOrder:   int64(l.order),
			AgentIgnore: agentIgnore,
			IsTerminal:  isTerminal,
		}); err != nil {
			return fmt.Errorf("create label %s: %w", l.name, err)
		}
	}

	sp := func(s string) *string { return &s }

	transitions := []struct {
		from        string
		to          string
		triggerType string
		path        *string
	}{
		{"agent-review", "review", "agent", sp("success")},
		{"agent-review", "work", "agent", sp("failure")},
		{"not_ready", "plan", "human", nil},
		{"plan", "review-plan", "agent", sp("success")},
		{"review", "done", "human", sp("success")},
		{"review", "work", "human", sp("failure")},
		{"review-plan", "plan", "human", sp("failure")},
		{"review-plan", "work", "human", sp("success")},
		{"testing", "agent-review", "agent", sp("success")},
		{"testing", "work", "agent", sp("failure")},
		{"work", "testing", "agent", sp("success")},
	}

	for _, t := range transitions {
		if _, err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
			ID:          uuid.NewString(),
			WorkflowID:  wfID,
			FromLabel:   t.from,
			ToLabel:     t.to,
			TriggerType: t.triggerType,
			Path:        t.path,
		}); err != nil {
			return fmt.Errorf("create transition %s→%s: %w", t.from, t.to, err)
		}
	}

	return nil
}
