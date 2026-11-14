package storage

import (
	"context"
	"database/sql"
	"errors"
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

	// "review" sets create_pr so the happy path (Approve -> done) ends with a PR
	// rather than a local-only branch. Best-effort: repos without a forge remote
	// just log and skip (see Engine.OnCreatePR).
	labels := []struct {
		name        string
		display     string
		color       string
		order       int
		agentIgnore bool
		terminal    bool
		createPR    bool
	}{
		{"not_ready", "Not ready", "#6B7280", 0, true, false, false},
		{"plan", "Plan", "#8B5CF6", 1, false, false, false},
		{"review-plan", "Plan review", "#3B82F6", 2, false, false, false},
		{"work", "Work", "#F59E0B", 3, false, false, false},
		{"testing", "Testing", "#F97316", 4, false, false, false},
		{"agent-review", "Agent review", "#6366F1", 5, false, false, false},
		{"review", "Human review", "#EC4899", 6, false, false, true},
		{"done", "Done", "#10B981", 7, false, true, false},
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
		createPR := int64(0)
		if l.createPR {
			createPR = 1
		}
		if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
			ID:          uuid.NewString(),
			WorkflowID:  wfID,
			Name:        l.name,
			DisplayName: l.display,
			Color:       l.color,
			SortOrder:   int64(l.order),
			AgentIgnore: agentIgnore,
			IsTerminal:  isTerminal,
			CreatePr:    createPR,
			VerifyMode:  "inherit",
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

// PRReviewWorkflowName is the seeded workflow used by the review-only PR path.
const PRReviewWorkflowName = "PR review"

const prReviewSeedMarker = "pr_review_workflow"

// SeedPRReviewWorkflow creates the "PR review" workflow (pr-review agent label
// -> done, plus a needs-attention column for failed reviews, left for a human (not agent_ignore, since the engine forbids agent moves into agent_ignore labels)) once per install.
// A marker row makes it one-shot, so deleting the workflow is not undone on the
// next start. The label is named "pr-review", not "review": agent configs match
// on label name globally and "review" is the Default workflow's human gate.
func SeedPRReviewWorkflow(ctx context.Context, db *DB) error {
	q := gen.New(db.SQL())
	if n, err := q.HasSeedMarker(ctx, prReviewSeedMarker); err != nil {
		return fmt.Errorf("check seed marker: %w", err)
	} else if n > 0 {
		return nil
	}
	if _, err := q.GetWorkflowByName(ctx, PRReviewWorkflowName); err == nil {
		return q.AddSeedMarker(ctx, prReviewSeedMarker)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("get workflow: %w", err)
	}

	wfID := uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{
		ID:          wfID,
		Name:        PRReviewWorkflowName,
		Description: "Review-only: an agent reviews a pull request and posts comments on it",
	}); err != nil {
		return fmt.Errorf("create workflow: %w", err)
	}
	labels := []struct {
		name, display, color string
		order                int
		ignore, terminal     bool
		verify               string
	}{
		{"pr-review", "PR review", "#6366F1", 0, false, false, "disabled"},
		{"needs-attention", "Needs attention", "#EF4444", 1, false, false, "inherit"},
		{"done", "Done", "#10B981", 2, false, true, "inherit"},
	}
	for _, l := range labels {
		p := gen.CreateWorkflowLabelParams{
			ID: uuid.NewString(), WorkflowID: wfID, Name: l.name, DisplayName: l.display,
			Color: l.color, SortOrder: int64(l.order), VerifyMode: l.verify,
		}
		if l.ignore {
			p.AgentIgnore = 1
		}
		if l.terminal {
			p.IsTerminal = 1
		}
		if _, err := q.CreateWorkflowLabel(ctx, p); err != nil {
			return fmt.Errorf("create label %s: %w", l.name, err)
		}
	}
	sp := func(s string) *string { return &s }
	transitions := []struct {
		from, to, trigger string
		path              *string
	}{
		{"pr-review", "done", "agent", sp("success")},
		{"pr-review", "needs-attention", "agent", sp("failure")},
		{"needs-attention", "pr-review", "human", nil},
		{"done", "pr-review", "both", nil},
	}
	for _, t := range transitions {
		if _, err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
			ID: uuid.NewString(), WorkflowID: wfID, FromLabel: t.from, ToLabel: t.to,
			TriggerType: t.trigger, Path: t.path,
		}); err != nil {
			return fmt.Errorf("create transition %s->%s: %w", t.from, t.to, err)
		}
	}
	return q.AddSeedMarker(ctx, prReviewSeedMarker)
}
