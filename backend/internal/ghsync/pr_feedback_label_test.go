package ghsync

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// enablePRFeedbackLabel recreates the fixture workflow's labels with a pr-fix
// label, a human in_review -> pr-fix transition, and pr_feedback_label set on
// in_review.
func enablePRFeedbackLabel(t *testing.T, f *conflictFixture) {
	t.Helper()
	ctx := context.Background()
	wfID := f.task.WorkflowID
	if err := f.q.DeleteWorkflowLabels(ctx, wfID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"in_review", "work", "pr-fix"} {
		p := gen.CreateWorkflowLabelParams{ID: uuid.NewString(), WorkflowID: wfID, Name: name, Color: "#000"}
		if name == "in_review" {
			p.PrFeedbackLabel = "pr-fix"
		}
		if _, err := f.q.CreateWorkflowLabel(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
		ID: uuid.NewString(), WorkflowID: wfID, FromLabel: "in_review", ToLabel: "pr-fix", TriggerType: "human",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAutoTransition_UsesPRFeedbackLabelWhenSet(t *testing.T) {
	f := newConflictFixture(t, true)
	enablePRFeedbackLabel(t, f)
	f.head.Mergeable = forge.MergeableConflicting
	f.sweep(t, "pr_open")
	updated, err := f.q.GetTask(context.Background(), f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Label != "pr-fix" {
		t.Errorf("label = %q, want pr-fix", updated.Label)
	}
}

func TestAutoTransition_FailurePathWhenPRFeedbackLabelUnset(t *testing.T) {
	f := newConflictFixture(t, true)
	f.head.Mergeable = forge.MergeableConflicting
	f.sweep(t, "pr_open")
	updated, err := f.q.GetTask(context.Background(), f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Label != "work" {
		t.Errorf("label = %q, want work", updated.Label)
	}
}
